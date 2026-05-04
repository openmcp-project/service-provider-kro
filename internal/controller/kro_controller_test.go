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

	"github.com/fluxcd/pkg/apis/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1alpha1 "github.com/openmcp-project/service-provider-kro/api/v1alpha1"
)

func TestResourceStatus(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		wantPhase  apiv1alpha1.InstancePhase
		wantMsg    string
	}{
		{
			name:       "no conditions",
			conditions: nil,
			wantPhase:  apiv1alpha1.Progressing,
			wantMsg:    "",
		},
		{
			name: "ready true",
			conditions: []metav1.Condition{
				{
					Type:    meta.ReadyCondition,
					Status:  metav1.ConditionTrue,
					Message: "stored artifact for revision abc123",
				},
			},
			wantPhase: apiv1alpha1.Ready,
			wantMsg:   "",
		},
		{
			name: "ready false carries message",
			conditions: []metav1.Condition{
				{
					Type:    meta.ReadyCondition,
					Status:  metav1.ConditionFalse,
					Message: "install retries exhausted",
				},
			},
			wantPhase: apiv1alpha1.Progressing,
			wantMsg:   "install retries exhausted",
		},
		{
			name: "ready unknown carries message",
			conditions: []metav1.Condition{
				{
					Type:    meta.ReadyCondition,
					Status:  metav1.ConditionUnknown,
					Message: "reconciliation in progress",
				},
			},
			wantPhase: apiv1alpha1.Progressing,
			wantMsg:   "reconciliation in progress",
		},
		{
			name: "ready missing among other conditions",
			conditions: []metav1.Condition{
				{
					Type:   meta.StalledCondition,
					Status: metav1.ConditionFalse,
				},
			},
			wantPhase: apiv1alpha1.Progressing,
			wantMsg:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			phase, msg := resourceStatus(tc.conditions)
			if phase != tc.wantPhase {
				t.Errorf("phase: got %q, want %q", phase, tc.wantPhase)
			}
			if msg != tc.wantMsg {
				t.Errorf("message: got %q, want %q", msg, tc.wantMsg)
			}
		})
	}
}
