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
	"math/rand"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	crankxpkg "github.com/crossplane/crossplane/v2/cmd/crank/xpkg"
	internalxpkg "github.com/crossplane/crossplane/v2/internal/xpkg"
)

func TestLoadProjectArtifact(t *testing.T) {
	artifactFile := filepath.Join(t.TempDir(), "module-prometheus.xpkg")

	refs := map[name.Reference]v1.Image{
		mustTag(t, "ghcr.io/elastisys/module-prometheus:configuration"):                      newProjectPackageImage(t, 1, "amd64", ""),
		mustTag(t, "ghcr.io/elastisys/module-prometheus-render-kube-prometheus-stack:amd64"): newProjectPackageImage(t, 2, "amd64", "xpkg-test-2"),
		mustTag(t, "ghcr.io/elastisys/module-prometheus-render-kube-prometheus-stack:arm64"): newProjectPackageImage(t, 3, "arm64", "xpkg-test-3"),
		mustTag(t, "ghcr.io/elastisys/module-prometheus-render-prometheus-rules:amd64"):      newProjectPackageImage(t, 4, "amd64", ""),
	}

	if err := tarball.MultiRefWriteToFile(artifactFile, refs); err != nil {
		t.Fatalf("tarball.MultiRefWriteToFile() unexpected error: %v", err)
	}

	artifact, err := loadProjectArtifact(artifactFile)
	if err != nil {
		t.Fatalf("loadProjectArtifact() unexpected error: %v", err)
	}

	if got, want := artifact.configurationRepo.Name(), "ghcr.io/elastisys/module-prometheus"; got != want {
		t.Fatalf("loadProjectArtifact() configurationRepo = %q, want %q", got, want)
	}

	if got, want := len(artifact.functionImages), 2; got != want {
		t.Fatalf("loadProjectArtifact() function repo count = %d, want %d", got, want)
	}

	stackRepo := mustRepository(t, "ghcr.io/elastisys/module-prometheus-render-kube-prometheus-stack")
	if got, want := len(artifact.functionImages[stackRepo]), 2; got != want {
		t.Fatalf("loadProjectArtifact() stack image count = %d, want %d", got, want)
	}
}

func TestPushCmdRunPushesFunctionsBeforeConfiguration(t *testing.T) {
	artifactFile := writeArtifact(t, map[name.Reference]v1.Image{
		mustTag(t, "ghcr.io/elastisys/module-prometheus:configuration"):  newProjectPackageImage(t, 10, "amd64", ""),
		mustTag(t, "ghcr.io/elastisys/module-prometheus-render-b:amd64"): newProjectPackageImage(t, 11, "amd64", ""),
		mustTag(t, "ghcr.io/elastisys/module-prometheus-render-a:amd64"): newProjectPackageImage(t, 12, "amd64", ""),
		mustTag(t, "ghcr.io/elastisys/module-prometheus-render-a:arm64"): newProjectPackageImage(t, 13, "arm64", ""),
	})

	pusher := &fakeImagePusher{}
	cmd := pushCmd{
		PackageFile: artifactFile,
		Tag:         "v1.2.3",
		pusher:      pusher,
	}

	if err := cmd.Run(logging.NewNopLogger()); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}

	got := pusher.urls()
	want := []string{
		"ghcr.io/elastisys/module-prometheus-render-a:v1.2.3",
		"ghcr.io/elastisys/module-prometheus-render-b:v1.2.3",
		"ghcr.io/elastisys/module-prometheus:v1.2.3",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Run() push order = %v, want %v", got, want)
	}

	if got, want := pusher.calls[0].imageCount, 2; got != want {
		t.Fatalf("Run() first function image count = %d, want %d", got, want)
	}
}

func TestPushCmdRunAppliesRepositoryOverride(t *testing.T) {
	artifactFile := writeArtifact(t, map[name.Reference]v1.Image{
		mustTag(t, "ghcr.io/elastisys/module-prometheus:configuration"):           newProjectPackageImage(t, 20, "amd64", ""),
		mustTag(t, "ghcr.io/elastisys/module-prometheus-render-prometheus:amd64"): newProjectPackageImage(t, 21, "amd64", ""),
	})

	pusher := &fakeImagePusher{}
	cmd := pushCmd{
		PackageFile: artifactFile,
		Repository:  "ghcr.io/example/module-prometheus",
		Tag:         "v9.9.9",
		pusher:      pusher,
	}

	if err := cmd.Run(logging.NewNopLogger()); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}

	got := pusher.urls()
	want := []string{
		"ghcr.io/example/module-prometheus-render-prometheus:v9.9.9",
		"ghcr.io/example/module-prometheus:v9.9.9",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Run() override urls = %v, want %v", got, want)
	}
}

type fakeImagePusher struct {
	calls []fakePushCall
}

type fakePushCall struct {
	url        string
	imageCount int
}

func (f *fakeImagePusher) PushImages(_ logging.Logger, images []crankxpkg.PackageImage, url string, _ ...crankxpkg.PushOption) error {
	f.calls = append(f.calls, fakePushCall{url: url, imageCount: len(images)})
	return nil
}

func (f *fakeImagePusher) urls() []string {
	urls := make([]string, 0, len(f.calls))
	for _, call := range f.calls {
		urls = append(urls, call.url)
	}
	return urls
}

func writeArtifact(t *testing.T, refs map[name.Reference]v1.Image) string {
	t.Helper()

	artifactFile := filepath.Join(t.TempDir(), "project.xpkg")
	if err := tarball.MultiRefWriteToFile(artifactFile, refs); err != nil {
		t.Fatalf("tarball.MultiRefWriteToFile() unexpected error: %v", err)
	}

	return artifactFile
}

func newProjectPackageImage(t *testing.T, seed int64, arch, annotation string) v1.Image {
	t.Helper()

	img, err := random.Image(512, 1, random.WithSource(rand.NewSource(seed)))
	if err != nil {
		t.Fatalf("random.Image() unexpected error: %v", err)
	}

	cfg, err := img.ConfigFile()
	if err != nil {
		t.Fatalf("img.ConfigFile() unexpected error: %v", err)
	}

	cfg.Architecture = arch
	cfg.OS = "linux"
	if cfg.Config.Labels == nil {
		cfg.Config.Labels = map[string]string{}
	}

	if annotation != "" {
		layers, err := img.Layers()
		if err != nil {
			t.Fatalf("img.Layers() unexpected error: %v", err)
		}

		dgst, err := layers[0].Digest()
		if err != nil {
			t.Fatalf("layers[0].Digest() unexpected error: %v", err)
		}

		cfg.Config.Labels[internalxpkg.Label(dgst.String())] = annotation
	}

	img, err = mutate.ConfigFile(img, cfg)
	if err != nil {
		t.Fatalf("mutate.ConfigFile() unexpected error: %v", err)
	}

	return img
}

func mustTag(t *testing.T, ref string) name.Reference {
	t.Helper()

	tag, err := name.NewTag(ref, name.StrictValidation)
	if err != nil {
		t.Fatalf("name.NewTag(%q) unexpected error: %v", ref, err)
	}

	return tag
}

func mustRepository(t *testing.T, ref string) name.Repository {
	t.Helper()

	repo, err := name.NewRepository(ref, name.StrictValidation)
	if err != nil {
		t.Fatalf("name.NewRepository(%q) unexpected error: %v", ref, err)
	}

	return repo
}
