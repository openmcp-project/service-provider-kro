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
	commonapi "github.com/openmcp-project/openmcp-operator/api/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KroSpec defines the desired state of Kro
type KroSpec struct {
	// Version is the version of the controller to install
	Version string `json:"version"`
}

// KroStatus defines the observed state of Kro.
type KroStatus struct {
	commonapi.Status `json:",inline"`

	// TODO: We might add a tracking of managed resources
	//       (See https://github.com/openmcp-project/service-provider-external-secrets/blob/d905cfca93af3c7d36250a4362a15d0447e858ec/api/v1alpha1/externalsecretsoperator_types.go#L56)
}

// Kro is the Schema for the kros API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=`.status.phase`,name="Phase",type=string
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:metadata:labels="openmcp.cloud/cluster=onboarding"
type Kro struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Kro
	// +required
	Spec KroSpec `json:"spec"`

	// status defines the observed state of Kro
	// +optional
	Status KroStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// KroList contains a list of Kro
type KroList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Kro `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Kro{}, &KroList{})
}

// Finalizer returns the finalizer string for the Kro resource
func (o *Kro) Finalizer() string {
	return GroupVersion.Group + "/finalizer"
}

// GetStatus returns the status of the Kro resource
func (o *Kro) GetStatus() any {
	return o.Status
}

// GetConditions returns the conditions of the Kro resource
func (o *Kro) GetConditions() *[]metav1.Condition {
	return &o.Status.Conditions
}

// SetPhase sets the phase of the Kro resource status
func (o *Kro) SetPhase(phase string) {
	o.Status.Phase = phase
}

// SetObservedGeneration sets the observed generation of the Kro resource
func (o *Kro) SetObservedGeneration(gen int64) {
	o.Status.ObservedGeneration = gen
}
