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
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// HelmValues captures the subset of the kro chart values the provider needs to act on
// during reconciliation. Unknown fields are preserved in the raw values passed through
// to the HelmRelease; this struct is only used to read, not rewrite.
type HelmValues struct {
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
}

// ExtractHelmValues parses the relevant image pull secret refs out of a ProviderConfig's
// raw Helm values. Nil and empty inputs yield a zero-value struct.
func ExtractHelmValues(values *apiextensionsv1.JSON) (*HelmValues, error) {
	out := &HelmValues{}
	if values == nil || len(values.Raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(values.Raw, out); err != nil {
		return nil, fmt.Errorf("failed to parse helm values: %w", err)
	}
	return out, nil
}
