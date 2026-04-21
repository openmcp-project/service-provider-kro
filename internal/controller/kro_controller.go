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
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	apiv1alpha1 "github.com/openmcp-project/service-provider-kro/api/v1alpha1"
	"github.com/openmcp-project/service-provider-kro/pkg/spruntime"
)

// KroReconciler reconciles a Kro object
type KroReconciler struct {
	// OnboardingCluster is the cluster where this controller watches Kro resources and reacts to their changes.
	OnboardingCluster *clusters.Cluster
	// PlatformCluster is the cluster where this controller is deployed and configured.
	PlatformCluster *clusters.Cluster
	// PodNamespace is the namespace where this controller is deployed in.
	PodNamespace string
}

// CreateOrUpdate is called on every add or update event
func (r *KroReconciler) CreateOrUpdate(ctx context.Context, svcobj *apiv1alpha1.Kro, _ *apiv1alpha1.ProviderConfig, clusters spruntime.ClusterContext) (ctrl.Result, error) {
	// TODO
	return ctrl.Result{}, nil
}

// Delete is called on every delete event
func (r *KroReconciler) Delete(ctx context.Context, obj *apiv1alpha1.Kro, _ *apiv1alpha1.ProviderConfig, clusters spruntime.ClusterContext) (ctrl.Result, error) {
	// TODO
	return ctrl.Result{}, nil
}
