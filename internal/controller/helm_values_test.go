/*
Copyright 2025.

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

package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	apiv1alpha1 "github.com/openmcp-project/service-provider-kro/api/v1alpha1"
)

func TestExtractHelmValues(t *testing.T) {
	tests := []struct {
		name    string
		input   *apiextensionsv1.JSON
		want    []corev1.LocalObjectReference
		wantErr bool
	}{
		{
			name:  "nil values",
			input: nil,
			want:  nil,
		},
		{
			name:  "empty raw",
			input: &apiextensionsv1.JSON{Raw: nil},
			want:  nil,
		},
		{
			name:  "empty object",
			input: &apiextensionsv1.JSON{Raw: []byte(`{}`)},
			want:  nil,
		},
		{
			name:  "values without pull secrets",
			input: &apiextensionsv1.JSON{Raw: []byte(`{"deployment":{"replicas":3}}`)},
			want:  nil,
		},
		{
			name:  "single pull secret",
			input: &apiextensionsv1.JSON{Raw: []byte(`{"imagePullSecrets":[{"name":"regcred"}]}`)},
			want:  []corev1.LocalObjectReference{{Name: "regcred"}},
		},
		{
			name:  "multiple pull secrets",
			input: &apiextensionsv1.JSON{Raw: []byte(`{"imagePullSecrets":[{"name":"a"},{"name":"b"}]}`)},
			want:  []corev1.LocalObjectReference{{Name: "a"}, {Name: "b"}},
		},
		{
			name:  "unknown fields ignored",
			input: &apiextensionsv1.JSON{Raw: []byte(`{"imagePullSecrets":[{"name":"a"}],"unknown":true,"extra":42}`)},
			want:  []corev1.LocalObjectReference{{Name: "a"}},
		},
		{
			name:    "malformed json",
			input:   &apiextensionsv1.JSON{Raw: []byte(`{not json`)},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractHelmValues(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got.ImagePullSecrets) != len(tc.want) {
				t.Fatalf("ImagePullSecrets length: got %d, want %d", len(got.ImagePullSecrets), len(tc.want))
			}
			for i, ref := range got.ImagePullSecrets {
				if ref.Name != tc.want[i].Name {
					t.Errorf("ImagePullSecrets[%d].Name: got %q, want %q", i, ref.Name, tc.want[i].Name)
				}
			}
		})
	}
}

func TestResolveHelmValues(t *testing.T) {
	tests := []struct {
		name      string
		input     *apiextensionsv1.JSON
		requested apiv1alpha1.RBACMode
		locked    bool
		wantMode  apiv1alpha1.RBACMode
		wantRaw   string
		wantErr   bool
	}{
		{
			name:     "nil values fall back to default",
			input:    nil,
			wantMode: apiv1alpha1.DefaultRBACMode,
			wantRaw:  `{"rbac":{"mode":"unrestricted"}}`,
		},
		{
			name:     "null values fall back to default",
			input:    &apiextensionsv1.JSON{Raw: []byte(`null`)},
			wantMode: apiv1alpha1.DefaultRBACMode,
			wantRaw:  `{"rbac":{"mode":"unrestricted"}}`,
		},
		{
			name:     "provider config mode used when not requested",
			input:    &apiextensionsv1.JSON{Raw: []byte(`{"rbac":{"mode":"aggregation"}}`)},
			wantMode: apiv1alpha1.RBACModeAggregation,
			wantRaw:  `{"rbac":{"mode":"aggregation"}}`,
		},
		{
			name:      "requested mode wins over provider config",
			input:     &apiextensionsv1.JSON{Raw: []byte(`{"rbac":{"mode":"aggregation"}}`)},
			requested: apiv1alpha1.RBACModeUnrestricted,
			wantMode:  apiv1alpha1.RBACModeUnrestricted,
			wantRaw:   `{"rbac":{"mode":"unrestricted"}}`,
		},
		{
			name:      "other values preserved",
			input:     &apiextensionsv1.JSON{Raw: []byte(`{"deployment":{"replicaCount":9007199254740993},"rbac":{"extra":true},"imagePullSecrets":[{"name":"a"}]}`)},
			requested: apiv1alpha1.RBACModeAggregation,
			wantMode:  apiv1alpha1.RBACModeAggregation,
			wantRaw:   `{"deployment":{"replicaCount":9007199254740993},"imagePullSecrets":[{"name":"a"}],"rbac":{"extra":true,"mode":"aggregation"}}`,
		},
		{
			name:     "null rbac treated as unset",
			input:    &apiextensionsv1.JSON{Raw: []byte(`{"rbac":null}`)},
			wantMode: apiv1alpha1.DefaultRBACMode,
			wantRaw:  `{"rbac":{"mode":"unrestricted"}}`,
		},
		{
			name:     "locked without request uses provider config mode",
			input:    &apiextensionsv1.JSON{Raw: []byte(`{"rbac":{"mode":"aggregation"}}`)},
			locked:   true,
			wantMode: apiv1alpha1.RBACModeAggregation,
			wantRaw:  `{"rbac":{"mode":"aggregation"}}`,
		},
		{
			name:      "locked with matching request",
			input:     &apiextensionsv1.JSON{Raw: []byte(`{"rbac":{"mode":"aggregation"}}`)},
			requested: apiv1alpha1.RBACModeAggregation,
			locked:    true,
			wantMode:  apiv1alpha1.RBACModeAggregation,
			wantRaw:   `{"rbac":{"mode":"aggregation"}}`,
		},
		{
			name:      "locked with differing request",
			input:     &apiextensionsv1.JSON{Raw: []byte(`{"rbac":{"mode":"aggregation"}}`)},
			requested: apiv1alpha1.RBACModeUnrestricted,
			locked:    true,
			wantErr:   true,
		},
		{
			name:      "locked to default when provider config sets no mode",
			input:     nil,
			requested: apiv1alpha1.RBACModeAggregation,
			locked:    true,
			wantErr:   true,
		},
		{
			name:    "non-object rbac",
			input:   &apiextensionsv1.JSON{Raw: []byte(`{"rbac":"aggregation"}`)},
			wantErr: true,
		},
		{
			name:    "malformed json",
			input:   &apiextensionsv1.JSON{Raw: []byte(`{not json`)},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, mode, err := ResolveHelmValues(tc.input, tc.requested, tc.locked)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mode != tc.wantMode {
				t.Errorf("mode: got %q, want %q", mode, tc.wantMode)
			}
			if string(got.Raw) != tc.wantRaw {
				t.Errorf("raw: got %s, want %s", got.Raw, tc.wantRaw)
			}
		})
	}
}
