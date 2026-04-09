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
	"testing"

	"k8s.io/utils/ptr"

	pkgmetav1 "github.com/crossplane/crossplane/apis/v2/pkg/meta/v1"
)

func TestFunctionPackageRef(t *testing.T) {
	tests := map[string]struct {
		dep  pkgmetav1.Dependency
		want string
	}{
		"LegacyFunctionAddsVersion": {
			dep: pkgmetav1.Dependency{
				Function: ptr.To("xpkg.crossplane.io/crossplane-contrib/function-extra-resources"),
				Version:  "v0.2.0",
			},
			want: "xpkg.crossplane.io/crossplane-contrib/function-extra-resources:v0.2.0",
		},
		"LegacyFunctionKeepsTaggedReference": {
			dep: pkgmetav1.Dependency{
				Function: ptr.To("xpkg.crossplane.io/crossplane-contrib/function-auto-ready:v0.5.0"),
				Version:  "v0.6.0",
			},
			want: "xpkg.crossplane.io/crossplane-contrib/function-auto-ready:v0.5.0",
		},
		"LegacyFunctionKeepsDigestReference": {
			dep: pkgmetav1.Dependency{
				Function: ptr.To("xpkg.crossplane.io/crossplane-contrib/function-auto-ready@sha256:abc123"),
				Version:  "v0.6.0",
			},
			want: "xpkg.crossplane.io/crossplane-contrib/function-auto-ready@sha256:abc123",
		},
		"PackageDependencyAddsVersion": {
			dep: pkgmetav1.Dependency{
				Package: ptr.To("xpkg.crossplane.io/crossplane-contrib/function-auto-ready"),
				Version: "v0.5.0",
			},
			want: "xpkg.crossplane.io/crossplane-contrib/function-auto-ready:v0.5.0",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := FunctionPackageRef(tc.dep); got != tc.want {
				t.Fatalf("FunctionPackageRef() = %q, want %q", got, tc.want)
			}
		})
	}
}
