/*
Copyright 2023 The Crossplane Authors.

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

package xpkg

import (
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/spf13/afero"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	"github.com/crossplane/crossplane/v2/internal/xpkg"
)

const (
	errGetwd           = "failed to get working directory while searching for package"
	errFindPackageinWd = "failed to find a package in current working directory"
)

// pushCmd pushes a package.
type pushCmd struct {
	// Arguments.
	Package string `arg:"" help:"Where to push the package. Must be a fully qualified OCI tag, including the registry, repository, and tag." placeholder:"REGISTRY/REPOSITORY:TAG"`

	// Flags. Keep sorted alphabetically.
	InsecureSkipTLSVerify bool     `help:"[INSECURE] Skip verifying TLS certificates."`
	PackageFiles          []string `help:"A comma-separated list of xpkg files to push." placeholder:"PATH" predictor:"xpkg_file" short:"f" type:"existingfile"`

	// Internal state. These aren't part of the user-exposed CLI structure.
	fs afero.Fs
}

func (c *pushCmd) Help() string {
	return `
Packages can be pushed to any OCI registry. A package's OCI tag must be a semantic
version. Credentials for the registry are automatically retrieved from xpkg login
and dockers configuration as fallback.

IMPORTANT: the package must be fully qualified, including the registry, repository, and tag.

Examples:

  # Push a multi-platform package.
  crossplane xpkg push -f function-amd64.xpkg,function-arm64.xpkg xpkg.crossplane.io/crossplane/function-example:v1.0.0

  # Push the xpkg file in the current directory to a different registry.
  crossplane xpkg push index.docker.io/crossplane/function-example:v1.0.0
`
}

// AfterApply sets the tag for the parent push command.
func (c *pushCmd) AfterApply() error {
	c.fs = afero.NewOsFs()
	return nil
}

// Run runs the push cmd.
func (c *pushCmd) Run(logger logging.Logger) error {
	// If package is not defined, attempt to find single package in current
	// directory.
	if len(c.PackageFiles) == 0 {
		wd, err := os.Getwd()
		if err != nil {
			return errors.Wrap(err, errGetwd)
		}

		path, err := xpkg.FindXpkgInDir(c.fs, wd)
		if err != nil {
			return errors.Wrap(err, errFindPackageinWd)
		}

		c.PackageFiles = []string{path}
		logger.Debug("Found package in directory", "path", path)
	}

	// load images from all the provided package files
	images := make([]PackageImage, 0, len(c.PackageFiles))
	for _, p := range c.PackageFiles {
		cleanPath := filepath.Clean(p)

		img, err := tarball.ImageFromPath(cleanPath, nil)
		if err != nil {
			return err
		}

		images = append(images, PackageImage{Image: img, Path: cleanPath})
	}

	pusher := NewImagePusher()
	return pusher.PushImages(
		logger,
		images,
		c.Package,
		WithRemoteOptions(NewRemoteOptions(c.InsecureSkipTLSVerify)...),
	)
}
