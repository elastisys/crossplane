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
	"path/filepath"

	"github.com/alecthomas/kong"
	"github.com/spf13/afero"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	devv1alpha1 "github.com/crossplane/crossplane/apis/v2/dev/v1alpha1"
)

// buildCmd builds a project into Crossplane packages.
type buildCmd struct {
	ProjectFile    string `default:"crossplane-project.yaml"                   help:"Path to project definition."     short:"f"`
	Repository     string `help:"Override the repository in the project file." optional:""`
	OutputDir      string `default:"_output"                                   help:"Output directory for packages."  short:"o"`
	MaxConcurrency uint   `default:"8"                                         help:"Max concurrent function builds."`

	proj   *devv1alpha1.Project
	projFS afero.Fs
}

// AfterApply parses flags and reads the project file.
func (c *buildCmd) AfterApply() error {
	pc, err := loadProjectContext(c.ProjectFile)
	if err != nil {
		return err
	}
	c.proj = pc.proj
	c.projFS = pc.fs

	return nil
}

// Run executes the build command.
func (c *buildCmd) Run(_ *kong.Context, logger logging.Logger) error {
	outFile := filepath.Join(c.OutputDir, fmt.Sprintf("%s.xpkg", c.proj.Name))
	if _, err := buildProjectArtifact(context.Background(), logger, c.ProjectFile, c.Repository, outFile, c.MaxConcurrency); err != nil {
		return err
	}

	fmt.Printf("Built project %q to %s\n", c.proj.Name, outFile) //nolint:forbidigo // CLI output.

	return nil
}
