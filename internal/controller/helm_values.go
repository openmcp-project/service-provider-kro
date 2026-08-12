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

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/apis/kustomize"
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

// kubeconfigPostRenderers returns the Flux PostRenderers that inject the
// KUBECONFIG environment variable, volume, and volumeMount into the kro
// deployment. The kro chart does not template extraVolumes/extraVolumeMounts/extraEnv,
// so we patch everything via kustomize strategic merge.
func kubeconfigPostRenderers() []helmv2.PostRenderer {
	kubeconfigFilePath := mcpKubeconfigMountPath + "/" + mcpKubeconfigKey
	patch := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: kro
spec:
  template:
    spec:
      containers:
        - name: kro
          env:
            - name: KUBECONFIG
              value: %s
          volumeMounts:
            - name: mcp-kubeconfig
              mountPath: %s
              readOnly: true
      volumes:
        - name: mcp-kubeconfig
          secret:
            secretName: %s`, kubeconfigFilePath, mcpKubeconfigMountPath, mcpKubeconfigSecretName)

	return []helmv2.PostRenderer{
		{
			Kustomize: &helmv2.Kustomize{
				Patches: []kustomize.Patch{
					{
						Patch: patch,
						Target: &kustomize.Selector{
							Group:   "apps",
							Version: "v1",
							Kind:    "Deployment",
						},
					},
				},
			},
		},
	}
}

// crdOnlyPostRenderers returns PostRenderers that strip all non-CRD resources
// from the rendered chart. Used for the MCP-targeting HelmRelease that only
// needs to install CRDs (from the chart's crds/ directory).
func crdOnlyPostRenderers() []helmv2.PostRenderer {
	targets := []kustomize.Patch{
		{
			Patch: "$patch: delete\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: kro\n",
			Target: &kustomize.Selector{
				Group:   "apps",
				Version: "v1",
				Kind:    "Deployment",
			},
		},
		{
			Patch: "$patch: delete\napiVersion: v1\nkind: ServiceAccount\nmetadata:\n  name: kro\n",
			Target: &kustomize.Selector{
				Version: "v1",
				Kind:    "ServiceAccount",
			},
		},
		{
			Patch: "$patch: delete\napiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: kro\n",
			Target: &kustomize.Selector{
				Group:   "rbac.authorization.k8s.io",
				Version: "v1",
				Kind:    "ClusterRole",
			},
		},
		{
			Patch: "$patch: delete\napiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRoleBinding\nmetadata:\n  name: kro\n",
			Target: &kustomize.Selector{
				Group:   "rbac.authorization.k8s.io",
				Version: "v1",
				Kind:    "ClusterRoleBinding",
			},
		},
		{
			Patch: "$patch: delete\napiVersion: v1\nkind: Service\nmetadata:\n  name: kro\n",
			Target: &kustomize.Selector{
				Version: "v1",
				Kind:    "Service",
			},
		},
	}

	return []helmv2.PostRenderer{
		{
			Kustomize: &helmv2.Kustomize{
				Patches: targets,
			},
		},
	}
}
