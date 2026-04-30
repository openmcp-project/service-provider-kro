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
