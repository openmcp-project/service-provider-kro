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

package v1alpha1

import (
	"fmt"
	"strings"
	"time"

	ctrlerrors "github.com/openmcp-project/controller-utils/pkg/errors"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// DefaultChartURL points to the default location of where the kro chart lives.
	DefaultChartURL = "oci://registry.k8s.io/kro/charts/kro"
	// DefaultPollInterval is used when a ProviderConfig does not set spec.pollInterval.
	DefaultPollInterval = time.Minute
)

// ErrVersionNotAvailable indicates that a version requested through Kro.Spec.Version is not
// offered by the ProviderConfig.
var ErrVersionNotAvailable = fmt.Errorf("%w: requested version is not available", ctrlerrors.ErrInvalidUserInput)

// ProviderConfigSpec defines the desired state of ProviderConfig
type ProviderConfigSpec struct {
	// Versions enumerates the kro versions that tenants may request through
	// Kro.Spec.Version, and maps each of them to the artifacts used to install it.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=version
	Versions []KroVersion `json:"versions"`

	// PollInterval at which the controller requeues to detect drift.
	// +optional
	// +kubebuilder:default:="1m"
	// +kubebuilder:validation:Format=duration
	PollInterval *metav1.Duration `json:"pollInterval,omitempty"`
}

// KroVersion defines a version of kro that can be installed.
type KroVersion struct {
	// Version is the kro version to install.
	// +required
	Version string `json:"version"`

	// ChartVersion is the tag of the Helm chart to install.
	// +required
	ChartVersion string `json:"chartVersion"`

	// ChartURL is a reference to an OCI repository that hosts the kro Helm chart.
	// An "oci://" prefix is added automatically when missing.
	// +optional
	// +kubebuilder:default="oci://registry.k8s.io/kro/charts/kro"
	ChartURL *string `json:"chartURL,omitempty"`

	// ChartPullSecret is the name of a secret in the controller's namespace holding the
	// credentials to pull the Helm chart. It is replicated into the tenant namespace and
	// wired as secretRef on the OCIRepository.
	// The secret must be of type kubernetes.io/dockerconfigjson.
	// +optional
	ChartPullSecret string `json:"chartPullSecret,omitempty"`

	// HelmValues are arbitrary Helm values passed directly to the managed HelmRelease.
	// Secrets referenced under `imagePullSecrets` are replicated from the controller's
	// namespace into the kro namespace on the control plane.
	// +optional
	HelmValues *apiextensionsv1.JSON `json:"helmValues,omitempty"`
}

// ProviderConfigStatus defines the observed state of ProviderConfig.
type ProviderConfigStatus struct {
	// Conditions represent the current state of the ProviderConfig resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ProviderConfig is the Schema for the providerconfigs API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:metadata:labels="openmcp.cloud/cluster=platform"
type ProviderConfig struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ProviderConfig
	// +required
	Spec ProviderConfigSpec `json:"spec"`

	// status defines the observed state of ProviderConfig
	// +optional
	Status ProviderConfigStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ProviderConfigList contains a list of ProviderConfig
type ProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &ProviderConfig{}, &ProviderConfigList{})
		return nil
	})
}

// PollInterval returns the poll interval duration from the spec, falling back to
// DefaultPollInterval when unset.
func (o *ProviderConfig) PollInterval() time.Duration {
	if o == nil || o.Spec.PollInterval == nil {
		return DefaultPollInterval
	}
	return o.Spec.PollInterval.Duration
}

// ResolveVersion returns the version entry offering the requested tenant facing version.
func (o *ProviderConfig) ResolveVersion(version string) (KroVersion, error) {
	if o == nil {
		return KroVersion{}, fmt.Errorf("%w: %q, no provider config is configured", ErrVersionNotAvailable, version)
	}
	availableVersions := make([]string, 0, len(o.Spec.Versions))
	for _, candidate := range o.Spec.Versions {
		if candidate.Version == version {
			return candidate, nil
		}

		availableVersions = append(availableVersions, candidate.Version)
	}
	if len(availableVersions) == 0 {
		return KroVersion{}, fmt.Errorf("%w: %q, the provider config offers no versions", ErrVersionNotAvailable, version)
	}
	return KroVersion{}, fmt.Errorf("%w: %q, available versions are: %s", ErrVersionNotAvailable, version, strings.Join(availableVersions, ", "))
}

// GetChartURL returns the chart URL of this version with an "oci://" scheme, falling back
// to DefaultChartURL when unset.
func (v KroVersion) GetChartURL() string {
	if v.ChartURL == nil || *v.ChartURL == "" {
		return DefaultChartURL
	}
	return ensureOCIScheme(*v.ChartURL)
}

// ensureOCIScheme prefixes the given URL with "oci://" unless it already has the scheme.
func ensureOCIScheme(url string) string {
	if !strings.HasPrefix(url, "oci://") {
		return "oci://" + url
	}
	return url
}
