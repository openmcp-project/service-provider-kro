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

package spruntime

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	"github.com/openmcp-project/controller-utils/pkg/controller"
)

// PCReconciler notifies the service provider about provider config updates
// through a shared update channel. Any provider config change results in a reconcile request
// for every existing service provider api object.
type PCReconciler[T ProviderConfig] struct {
	platformCluster       *clusters.Cluster
	providerUpdateChannel chan event.GenericEvent
	providerName          string
	emptyObj              func() T
}

// NewPCReconciler creates a new provider PCReconciler instance.
func NewPCReconciler[T ProviderConfig](providerName string, emptyObj func() T) *PCReconciler[T] {
	return &PCReconciler[T]{
		providerName: providerName,
		emptyObj:     emptyObj,
	}
}

// WithPlatformCluster sets the platform cluster.
func (r *PCReconciler[T]) WithPlatformCluster(c *clusters.Cluster) *PCReconciler[T] {
	r.platformCluster = c
	return r
}

// WithUpdateChannel sets the channel to send config changes.
func (r *PCReconciler[T]) WithUpdateChannel(c chan event.GenericEvent) *PCReconciler[T] {
	r.providerUpdateChannel = c
	return r
}

// Reconcile acts as a sender to notify receivers about provider config changes .
func (r *PCReconciler[T]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log.FromContext(ctx).Info("reconcile provider config")
	obj := r.emptyObj()
	notify := event.GenericEvent{}
	if err := r.platformCluster.Client().Get(ctx, req.NamespacedName, obj); err != nil {
		r.providerUpdateChannel <- notify
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		r.providerUpdateChannel <- notify
		return ctrl.Result{}, nil
	}
	notify.Object = obj.DeepCopyObject().(T)
	r.providerUpdateChannel <- notify
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PCReconciler[T]) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		WatchesRawSource(source.Kind(
			r.platformCluster.Cluster().GetCache(),
			r.emptyObj(),
			&handler.TypedEnqueueRequestForObject[T]{},
			controller.ToTypedPredicate[T](controller.ExactNamePredicate(r.providerName, "")),
		)).
		Named("providerconfig").
		Complete(r)
}
