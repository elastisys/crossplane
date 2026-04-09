/*
Copyright 2026 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package project

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	crankxpkg "github.com/crossplane/crossplane/v2/cmd/crank/xpkg"
	internalproject "github.com/crossplane/crossplane/v2/internal/project"
)

type imagePusher interface {
	PushImages(logger logging.Logger, images []crankxpkg.PackageImage, url string, opts ...crankxpkg.PushOption) error
}

type pushCmd struct {
	ProjectFile    string `default:"crossplane-project.yaml"                                       help:"Path to project definition."                                                        short:"f" type:"path"`
	Repository     string `help:"Override the repository in the project file."                     optional:""`
	PackageFile    string `help:"Use an existing built project package instead of building first." optional:""                 type:"path"`
	Tag            string `help:"Tag used when publishing the configuration and function packages." required:""                short:"t"`
	MaxConcurrency uint   `default:"8"                                                             help:"Max concurrent function builds and pushes."`

	pusher imagePusher
}

type projectArtifact struct {
	configurationRepo  name.Repository
	configurationImage crankxpkg.PackageImage
	functionImages     map[name.Repository][]crankxpkg.PackageImage
}

// Run executes the project push command.
func (c *pushCmd) Run(logger logging.Logger) error {
	pusher := c.pusher
	if pusher == nil {
		pusher = crankxpkg.NewImagePusher()
	}

	artifactFile, cleanup, err := c.packageFile(context.Background(), logger)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	artifact, err := loadProjectArtifact(artifactFile)
	if err != nil {
		return err
	}

	destConfigRepo := artifact.configurationRepo
	if c.Repository != "" {
		destConfigRepo, err = name.NewRepository(c.Repository, name.StrictValidation)
		if err != nil {
			return errors.Wrap(err, "failed to parse repository")
		}
	}

	pushOpts := []crankxpkg.PushOption{
		crankxpkg.WithRemoteOptions(crankxpkg.NewRemoteOptions(false)...),
		crankxpkg.WithMaxConcurrency(c.MaxConcurrency),
	}

	functionRepos := make([]name.Repository, 0, len(artifact.functionImages))
	for repo := range artifact.functionImages {
		functionRepos = append(functionRepos, repo)
	}
	slices.SortFunc(functionRepos, func(a, b name.Repository) int {
		return strings.Compare(a.Name(), b.Name())
	})

	for _, sourceRepo := range functionRepos {
		destRepo, err := functionDestinationRepository(artifact.configurationRepo, sourceRepo, destConfigRepo)
		if err != nil {
			return err
		}

		ref := destRepo.Tag(c.Tag)
		if err := pusher.PushImages(logger, artifact.functionImages[sourceRepo], ref.Name(), pushOpts...); err != nil {
			return errors.Wrapf(err, "cannot push function package %s", ref.Name())
		}
	}

	configRef := destConfigRepo.Tag(c.Tag)
	if err := pusher.PushImages(logger, []crankxpkg.PackageImage{artifact.configurationImage}, configRef.Name(), pushOpts...); err != nil {
		return errors.Wrapf(err, "cannot push configuration package %s", configRef.Name())
	}

	fmt.Printf("Pushed project to %s\n", configRef.Name()) //nolint:forbidigo // CLI output.
	return nil
}

func (c *pushCmd) packageFile(ctx context.Context, logger logging.Logger) (string, func(), error) {
	if c.PackageFile != "" {
		return filepath.Clean(c.PackageFile), nil, nil
	}

	tmpDir, err := os.MkdirTemp("", "crank-project-push-")
	if err != nil {
		return "", nil, errors.Wrap(err, "cannot create temporary build directory")
	}

	outFile := filepath.Join(tmpDir, "project.xpkg")
	if _, err := buildProjectArtifact(ctx, logger, c.ProjectFile, c.Repository, outFile, c.MaxConcurrency); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", nil, err
	}

	return outFile, func() { _ = os.RemoveAll(tmpDir) }, nil
}

func loadProjectArtifact(path string) (*projectArtifact, error) {
	cleanPath := filepath.Clean(path)
	opener := func() (io.ReadCloser, error) {
		return os.Open(cleanPath)
	}

	manifest, err := tarball.LoadManifest(opener)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load artifact manifest")
	}

	artifact := &projectArtifact{
		functionImages: map[name.Repository][]crankxpkg.PackageImage{},
	}

	for _, desc := range manifest {
		for _, repoTag := range desc.RepoTags {
			ref, err := name.NewTag(repoTag, name.StrictValidation)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to parse image reference %q", repoTag)
			}

			img, err := tarball.Image(opener, &ref)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to load image %q from artifact", repoTag)
			}

			pkgImg := crankxpkg.PackageImage{
				Image: img,
				Path:  fmt.Sprintf("%s[%s]", cleanPath, repoTag),
			}

			if ref.TagStr() == internalproject.ConfigurationTag {
				artifact.configurationRepo = ref.Repository
				artifact.configurationImage = pkgImg
				continue
			}

			artifact.functionImages[ref.Repository] = append(artifact.functionImages[ref.Repository], pkgImg)
		}
	}

	if artifact.configurationRepo.Name() == "" {
		return nil, errors.New("configuration image not found in artifact")
	}

	return artifact, nil
}

func functionDestinationRepository(sourceConfigRepo, sourceFunctionRepo, destConfigRepo name.Repository) (name.Repository, error) {
	if sourceConfigRepo.Name() == destConfigRepo.Name() {
		return sourceFunctionRepo, nil
	}

	sourceBase := sourceConfigRepo.Name()
	sourceFunction := sourceFunctionRepo.Name()
	if !strings.HasPrefix(sourceFunction, sourceBase+"-") {
		return name.Repository{}, errors.Errorf("function repository %q does not match project repository %q", sourceFunction, sourceBase)
	}

	return name.NewRepository(destConfigRepo.Name()+strings.TrimPrefix(sourceFunction, sourceBase), name.StrictValidation)
}
