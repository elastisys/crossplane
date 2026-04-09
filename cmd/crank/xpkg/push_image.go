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
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"golang.org/x/sync/errgroup"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	internalxpkg "github.com/crossplane/crossplane/v2/internal/xpkg"
)

const (
	errAnnotateLayers   = "failed to propagate xpkg annotations from OCI image config file to image layers"
	errFmtNewTag        = "failed to parse package tag %q"
	errFmtPushPackage   = "failed to push package file %s"
	errFmtGetDigest     = "failed to get digest of package file %s"
	errFmtNewDigest     = "failed to parse digest %q for package file %s"
	errFmtGetMediaType  = "failed to get media type of package file %s"
	errFmtGetConfigFile = "failed to get OCI config file of package file %s"
	errFmtWriteIndex    = "failed to push an OCI image index of %d packages"
)

// PackageImage describes a package image that will be pushed.
type PackageImage struct {
	// The OCI Image of the package to be pushed.
	Image v1.Image

	// Optional path for the image (for example a file path on disk) to help
	// provide more information about its source.
	Path string
}

type pushOptions struct {
	remoteOptions  []remote.Option
	maxConcurrency uint
}

// PushOption configures how images are pushed to a registry.
type PushOption func(*pushOptions)

// WithRemoteOptions sets the registry client options used when pushing.
func WithRemoteOptions(opts ...remote.Option) PushOption {
	return func(o *pushOptions) {
		o.remoteOptions = append([]remote.Option(nil), opts...)
	}
}

// WithMaxConcurrency limits the number of package images pushed at once when
// constructing a multi-architecture index.
func WithMaxConcurrency(n uint) PushOption {
	return func(o *pushOptions) {
		o.maxConcurrency = n
	}
}

// NewRemoteOptions returns the default remote options used by package pushes.
func NewRemoteOptions(insecureSkipTLSVerify bool) []remote.Option {
	t := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecureSkipTLSVerify, //nolint:gosec // CLI must support explicitly requested insecure connections.
		},
	}

	return []remote.Option{
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithTransport(t),
	}
}

// ImagePusher pushes one or more package images to an OCI registry.
type ImagePusher struct {
	write      func(name.Reference, v1.Image, ...remote.Option) error
	writeIndex func(name.Reference, v1.ImageIndex, ...remote.Option) error
}

// NewImagePusher returns an ImagePusher backed by go-containerregistry remote
// writes.
func NewImagePusher() *ImagePusher {
	return &ImagePusher{
		write:      remote.Write,
		writeIndex: remote.WriteIndex,
	}
}

// PushImages pushes package images to the given URL using the provided
// options.
func (p *ImagePusher) PushImages(logger logging.Logger, images []PackageImage, url string, opts ...PushOption) error { //nolint:gocognit // Shared OCI push path.
	o := &pushOptions{}
	for _, opt := range opts {
		opt(o)
	}

	if len(o.remoteOptions) == 0 {
		o.remoteOptions = []remote.Option{
			remote.WithAuthFromKeychain(authn.DefaultKeychain),
		}
	}

	tag, err := name.NewTag(url, name.StrictValidation)
	if err != nil {
		return errors.Wrapf(err, errFmtNewTag, url)
	}

	if len(images) == 1 {
		pi := images[0]

		img, err := internalxpkg.AnnotateLayers(pi.Image)
		if err != nil {
			return errors.Wrapf(err, errAnnotateLayers)
		}

		if err := p.write(tag, img, o.remoteOptions...); err != nil {
			return errors.Wrapf(err, errFmtPushPackage, pi.Path)
		}

		logger.Debug("Pushed package", "path", pi.Path, "ref", tag.String())

		return nil
	}

	adds := make([]mutate.IndexAddendum, len(images))

	limit := len(images)
	if o.maxConcurrency > 0 && int(o.maxConcurrency) < limit {
		limit = int(o.maxConcurrency)
	}
	if limit < 1 {
		limit = 1
	}

	sem := make(chan struct{}, limit)
	g, ctx := errgroup.WithContext(context.Background())
	for i, pi := range images {
		i, pi := i, pi
		g.Go(func() error {
			sem <- struct{}{}
			defer func() { <-sem }()

			img, err := internalxpkg.AnnotateLayers(pi.Image)
			if err != nil {
				return errors.Wrapf(err, errAnnotateLayers)
			}

			d, err := img.Digest()
			if err != nil {
				return errors.Wrapf(err, errFmtGetDigest, pi.Path)
			}

			n := fmt.Sprintf("%s@%s", tag.Repository.Name(), d.String())

			ref, err := name.NewDigest(n, name.StrictValidation)
			if err != nil {
				return errors.Wrapf(err, errFmtNewDigest, n, pi.Path)
			}

			mt, err := img.MediaType()
			if err != nil {
				return errors.Wrapf(err, errFmtGetMediaType, pi.Path)
			}

			conf, err := img.ConfigFile()
			if err != nil {
				return errors.Wrapf(err, errFmtGetConfigFile, pi.Path)
			}

			adds[i] = mutate.IndexAddendum{
				Add: img,
				Descriptor: v1.Descriptor{
					MediaType: mt,
					Platform: &v1.Platform{
						Architecture: conf.Architecture,
						OS:           conf.OS,
						OSVersion:    conf.OSVersion,
					},
				},
			}

			if err := p.write(ref, img, append(o.remoteOptions, remote.WithContext(ctx))...); err != nil {
				return errors.Wrapf(err, errFmtPushPackage, pi.Path)
			}

			logger.Debug("Pushed package", "path", pi.Path, "ref", ref.String())

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return err
	}

	if err := p.writeIndex(tag, mutate.AppendManifests(empty.Index, adds...), o.remoteOptions...); err != nil {
		return errors.Wrapf(err, errFmtWriteIndex, len(adds))
	}

	logger.Debug("Wrote OCI index", "ref", tag.String(), "manifests", len(adds))

	return nil
}
