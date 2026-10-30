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
	"bytes"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	apiv1alpha1 "github.com/openmcp-project/service-provider-kro/api/v1alpha1"
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

// ResolveHelmValues returns the ProviderConfig's raw Helm values with `rbac.mode` set to the
// effective mode, along with that mode. A requested mode overrides the ProviderConfig's, and
// DefaultRBACMode is used when nothing is set. When locked, a requested mode differing from
// the ProviderConfig's results in `ErrRBACModeLocked` error. All other values are preserved.
func ResolveHelmValues(values *apiextensionsv1.JSON, requested apiv1alpha1.RBACMode, locked bool) (*apiextensionsv1.JSON, apiv1alpha1.RBACMode, error) {
	parsed, rbac, err := fetchRBACValue(values)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch RBAC settings in values settings: %w", err)
	}

	configured := apiv1alpha1.DefaultRBACMode
	if m, ok := rbac["mode"].(string); ok && m != "" {
		configured = apiv1alpha1.RBACMode(m)
	}
	mode := configured
	if requested != "" {
		if locked && requested != configured {
			return nil, "", fmt.Errorf("%w: requested %q, the provider config enforces %q", apiv1alpha1.ErrRBACModeLocked, requested, configured)
		}
		mode = requested
	}
	rbac["mode"] = string(mode)
	parsed["rbac"] = rbac

	raw, err := json.Marshal(parsed)
	if err != nil {
		return nil, "", fmt.Errorf("failed to encode helm values: %w", err)
	}
	return &apiextensionsv1.JSON{Raw: raw}, mode, nil
}

// fetchRBACValue grabs the RBAC value from the parsed helm values and gives it back
// separately from the parsed values for overriding.
func fetchRBACValue(values *apiextensionsv1.JSON) (map[string]any, map[string]any, error) {
	parsed := map[string]any{}
	if values != nil && len(values.Raw) > 0 {
		dec := json.NewDecoder(bytes.NewReader(values.Raw))
		dec.UseNumber()
		if err := dec.Decode(&parsed); err != nil {
			return nil, nil, fmt.Errorf("failed to parse helm values: %w", err)
		}
		if parsed == nil {
			parsed = map[string]any{}
		}
	}

	var (
		rbac = make(map[string]any)
		ok   bool
	)
	if raw, exists := parsed["rbac"]; exists && raw != nil {
		rbac, ok = raw.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("helm value %q must be an object, got %T", "rbac", raw)
		}
	}

	return parsed, rbac, nil
}
