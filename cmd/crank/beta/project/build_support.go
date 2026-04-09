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
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/spf13/afero"
	"golang.org/x/term"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	devv1alpha1 "github.com/crossplane/crossplane/apis/v2/dev/v1alpha1"
	"github.com/crossplane/crossplane/v2/internal/async"
	internalproject "github.com/crossplane/crossplane/v2/internal/project"
	"github.com/crossplane/crossplane/v2/internal/project/functions"
	"github.com/crossplane/crossplane/v2/internal/schemas/generator"
	"github.com/crossplane/crossplane/v2/internal/schemas/manager"
	"github.com/crossplane/crossplane/v2/internal/schemas/runner"
	"github.com/crossplane/crossplane/v2/internal/terminal"
)

type projectContext struct {
	file string
	proj *devv1alpha1.Project
	fs   afero.Fs
}

func loadProjectContext(projectFile string) (*projectContext, error) {
	projFilePath, err := filepath.Abs(projectFile)
	if err != nil {
		return nil, err
	}

	projDirPath := filepath.Dir(projFilePath)
	projFS := afero.NewBasePathFs(afero.NewOsFs(), projDirPath)

	projFileName := filepath.Base(projFilePath)
	prj, err := internalproject.Parse(projFS, projFileName)
	if err != nil {
		return nil, errors.New("this is not a project directory")
	}

	return &projectContext{
		file: projFilePath,
		proj: prj,
		fs:   projFS,
	}, nil
}

func buildProjectArtifact(ctx context.Context, logger logging.Logger, projectFile, repository, outputFile string, maxConcurrency uint) (*devv1alpha1.Project, error) {
	pc, err := loadProjectContext(projectFile)
	if err != nil {
		return nil, err
	}

	proj := pc.proj.DeepCopy()
	if repository != "" {
		ref, err := name.NewRepository(repository)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse repository")
		}
		proj.Spec.Repository = ref.String()
	}

	concurrency := max(1, maxConcurrency)

	schemasFS := afero.NewBasePathFs(pc.fs, "schemas")
	schemaMgr := manager.New(
		schemasFS,
		generator.AllLanguages(),
		runner.NewRealSchemaRunner(runner.WithImageConfig(proj.Spec.ImageConfig)),
	)

	builder := internalproject.NewBuilder(
		internalproject.BuildWithMaxConcurrency(concurrency),
		internalproject.BuildWithFunctionIdentifier(functions.DefaultIdentifier),
		internalproject.BuildWithSchemaManager(schemaMgr),
	)

	pretty := term.IsTerminal(int(os.Stderr.Fd()))
	sp := terminal.NewSpinnerPrinter(os.Stderr, pretty)

	var imgMap internalproject.ImageTagMap
	if err := sp.WrapAsyncWithSuccessSpinners(func(ch async.EventChannel) error {
		var buildErr error
		imgMap, buildErr = builder.Build(ctx, proj, pc.fs,
			internalproject.BuildWithLogger(logger),
			internalproject.BuildWithEventChannel(ch),
		)
		return buildErr
	}); err != nil {
		return nil, err
	}

	if err := afero.NewOsFs().MkdirAll(filepath.Dir(outputFile), 0o755); err != nil {
		return nil, errors.Wrapf(err, "failed to create output directory %q", filepath.Dir(outputFile))
	}

	if err := tarball.MultiWriteToFile(outputFile, imgMap); err != nil {
		return nil, errors.Wrap(err, "failed to write package to file")
	}

	logger.Debug("Build complete", "output", outputFile)
	return proj, nil
}
