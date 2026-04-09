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

package xpkg

import (
	"fmt"
	"math/rand"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	internalxpkg "github.com/crossplane/crossplane/v2/internal/xpkg"
)

func TestImagePusherPushImagesWritesAnnotatedImage(t *testing.T) {
	server := httptest.NewTLSServer(registry.New())
	defer server.Close()

	ref := fmt.Sprintf("%s/example/package:v1.0.0", server.Listener.Addr().String())
	tag, err := name.NewTag(ref, name.StrictValidation)
	if err != nil {
		t.Fatalf("name.NewTag() unexpected error: %v", err)
	}

	img := newTestPackageImage(t, 11, "amd64", "xpkg-test-single")
	pusher := NewImagePusher()
	opts := []PushOption{
		WithRemoteOptions(remote.WithTransport(server.Client().Transport)),
	}

	if err := pusher.PushImages(logging.NewNopLogger(), []PackageImage{{Image: img, Path: "single.xpkg"}}, ref, opts...); err != nil {
		t.Fatalf("PushImages() unexpected error: %v", err)
	}

	remoteImg, err := remote.Image(tag, remote.WithTransport(server.Client().Transport))
	if err != nil {
		t.Fatalf("remote.Image() unexpected error: %v", err)
	}

	manifest, err := remoteImg.Manifest()
	if err != nil {
		t.Fatalf("remoteImg.Manifest() unexpected error: %v", err)
	}

	if got, want := manifest.Layers[0].Annotations[internalxpkg.AnnotationKey], "xpkg-test-single"; got != want {
		t.Fatalf("PushImages() layer annotation = %q, want %q", got, want)
	}
}

func TestImagePusherPushImagesWritesAnnotatedMultiArchIndex(t *testing.T) {
	server := httptest.NewTLSServer(registry.New())
	defer server.Close()

	ref := fmt.Sprintf("%s/example/package:v1.2.3", server.Listener.Addr().String())
	tag, err := name.NewTag(ref, name.StrictValidation)
	if err != nil {
		t.Fatalf("name.NewTag() unexpected error: %v", err)
	}

	images := []PackageImage{
		{Image: newTestPackageImage(t, 21, "amd64", "xpkg-test-amd64"), Path: "package-amd64.xpkg"},
		{Image: newTestPackageImage(t, 22, "arm64", "xpkg-test-arm64"), Path: "package-arm64.xpkg"},
	}

	pusher := NewImagePusher()
	opts := []PushOption{
		WithRemoteOptions(remote.WithTransport(server.Client().Transport)),
	}

	if err := pusher.PushImages(logging.NewNopLogger(), images, ref, opts...); err != nil {
		t.Fatalf("PushImages() unexpected error: %v", err)
	}

	index, err := remote.Index(tag, remote.WithTransport(server.Client().Transport))
	if err != nil {
		t.Fatalf("remote.Index() unexpected error: %v", err)
	}

	indexManifest, err := index.IndexManifest()
	if err != nil {
		t.Fatalf("index.IndexManifest() unexpected error: %v", err)
	}

	if got, want := len(indexManifest.Manifests), 2; got != want {
		t.Fatalf("len(indexManifest.Manifests) = %d, want %d", got, want)
	}

	gotArchAnnotations := map[string]string{}
	for _, desc := range indexManifest.Manifests {
		digestRef, err := name.NewDigest(fmt.Sprintf("%s@%s", tag.Repository.Name(), desc.Digest.String()), name.StrictValidation)
		if err != nil {
			t.Fatalf("name.NewDigest() unexpected error: %v", err)
		}

		img, err := remote.Image(digestRef, remote.WithTransport(server.Client().Transport))
		if err != nil {
			t.Fatalf("remote.Image() unexpected error: %v", err)
		}

		manifest, err := img.Manifest()
		if err != nil {
			t.Fatalf("img.Manifest() unexpected error: %v", err)
		}

		gotArchAnnotations[desc.Platform.Architecture] = manifest.Layers[0].Annotations[internalxpkg.AnnotationKey]
	}

	wantArchAnnotations := map[string]string{
		"amd64": "xpkg-test-amd64",
		"arm64": "xpkg-test-arm64",
	}
	if !reflect.DeepEqual(gotArchAnnotations, wantArchAnnotations) {
		t.Fatalf("PushImages() annotations = %v, want %v", gotArchAnnotations, wantArchAnnotations)
	}
}

func newTestPackageImage(t *testing.T, seed int64, arch, annotation string) v1.Image {
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
