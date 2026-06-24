package spruntime

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	controllerutil2 "github.com/openmcp-project/controller-utils/pkg/controller"
	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	apiconst "github.com/openmcp-project/openmcp-operator/api/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// ServiceProviderReconciler implements any business logic required to manage ServiceProviderAPI objects
type ServiceProviderReconciler[T ServiceProviderAPI, PC ProviderConfig] interface {
	// CreateOrUpdate is called on every add or update event
	CreateOrUpdate(ctx context.Context, obj T, pc PC, clusters ClusterContext) (ctrl.Result, error)
	// Delete is called on every delete event
	Delete(ctx context.Context, obj T, pc PC, clusters ClusterContext) (ctrl.Result, error)
}

// SecretWatcher can optionally be implemented by a ServiceProviderReconciler
// to trigger reconciliation of all ServiceProviderAPI objects when a
// referenced secret in the provider namespace changes.
// The watch is set up on the platform cluster and filtered to the namespace
// configured via WithSecretNamespace.
type SecretWatcher[PC ProviderConfig] interface {
	// IsReferencedSecret returns true if the given secret should trigger
	// reconciliation. pc is the current provider config — it will be the
	// zero value (nil for pointer types) if not yet loaded; implementations
	// must guard against this.
	IsReferencedSecret(ctx context.Context, secret *corev1.Secret, pc PC) bool
}

// ClusterContext provides access to request-scoped clusters.
// These clusters include the managed control plane and workload clusters associated with a specific reconcile request.
// (Static clusters like the platform and onboarding clusters are provided to the reconciler when it is initialized.)
//
// More info on the deployment model:
// https://openmcp-project.github.io/docs/about/design/service-provider#deployment-model
type ClusterContext struct {
	// MCPCluster is the managed control plane that belongs to the current reconcile request
	MCPCluster *clusters.Cluster
	// MCPAccessSecretKey provides the object key to retrieve the MCP kubeconfig secret
	MCPAccessSecretKey client.ObjectKey
	// WorkloadCluster is the workload cluster that belongs the current reconcile request
	WorkloadCluster *clusters.Cluster
	// WorkloadAccessSecretKey provides the object key to retrieve the workload cluster kubeconfig secret
	WorkloadAccessSecretKey client.ObjectKey
}

// ServiceProviderAPI represents the end-user facing onboarding api type
type ServiceProviderAPI interface {
	client.Object
	ServiceProviderAPIStatus
	Finalizer() string
}

// ServiceProviderAPIStatus represents the common status contract of ServiceProviderAPI types
type ServiceProviderAPIStatus interface {
	// GetStatus returns the status object
	GetStatus() any
	// GetConditions returns the status object
	GetConditions() *[]metav1.Condition
	// SetPhase sets Status.Phase
	SetPhase(string)
	// SetObservedGeneration sets Status.ObservedGeneration
	SetObservedGeneration(int64)
}

// ProviderConfig represents the config for platform operators
// The ProviderConfig is passed to the DomainServiceReconcile to reconcile APIObjects
type ProviderConfig interface {
	client.Object
	// PollIntveral can be used to periodically requeue, preventing managed objects
	// from drifting on the target cluster.  Return 0 if not required.
	PollInterval() time.Duration
}

// ClusterAccessProvider is a light weight version of the ClusterAccessReconciler
type ClusterAccessProvider interface {
	// MCPCluster creates a Cluster for the MCP AccessRequest.
	// This function will only be successful if the MCP AccessRequest is granted and Reconcile returned without an error
	// and a reconcile.Result with no RequeueAfter value.
	MCPCluster(ctx context.Context, request reconcile.Request) (*clusters.Cluster, error)
	// MCPAccessRequest returns the AccessRequest for the MCP cluster.
	MCPAccessRequest(ctx context.Context, request reconcile.Request) (*clustersv1alpha1.AccessRequest, error)
	// WorkloadCluster creates a Cluster for the Workload AccessRequest.
	// This function will only be successful if the Workload AccessRequest is granted and Reconcile returned without an error
	// and a reconcile.Result with no RequeueAfter value.
	WorkloadCluster(ctx context.Context, request reconcile.Request) (*clusters.Cluster, error)
	// WorkloadAccessRequest returns the AccessRequest for the Workload cluster.
	WorkloadAccessRequest(ctx context.Context, request reconcile.Request) (*clustersv1alpha1.AccessRequest, error)
	// Reconcile creates the ClusterRequests and AccessRequests for the MCP and Workload clusters based on the reconciled object.
	// This function should be called during all reconciliations of the reconciled object.
	// ctx is the context for the reconciliation.
	// request is the object that is being reconciled
	// It returns a reconcile.Result and an error if the reconciliation failed.
	// The reconcile.Result may contain a RequeueAfter value to indicate that the reconciliation should be retried after a certain duration.
	Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error)
	// ReconcileDelete deletes the AccessRequests and ClusterRequests for the MCP and Workload clusters based on the reconciled object.
	// This function should be called during the deletion of the reconciled object.
	// ctx is the context for the reconciliation.
	// request is the object that is being reconciled.
	// It returns a reconcile.Result and an error if the reconciliation failed.
	// The reconcile.Result may contain a RequeueAfter value to indicate that the reconciliation should be retried after a certain duration.
	ReconcileDelete(ctx context.Context, request reconcile.Request) (reconcile.Result, error)
}

// SPReconciler implements a generic reconcile loop to separate platform
// and service provider developer space.
type SPReconciler[T ServiceProviderAPI, PC ProviderConfig] struct {
	// platformCluster represents the platform cluster of the v2 architecture
	platformCluster *clusters.Cluster
	// onboardingCluster represents the onboarding cluster of the v2 architecture
	onboardingCluster *clusters.Cluster
	// clusterAccessReconciler reconciles access to MCP and workload clusters
	clusterAccessReconciler ClusterAccessProvider
	// serviceProviderReonciler reconciles the end-user facing onboarding API of a service provider
	serviceProviderReconciler ServiceProviderReconciler[T, PC]
	// providerConfig represents the platform operator facing platform API of a service provider
	providerConfig atomic.Pointer[PC]
	// withWorkloadCluster defines whether a service provider requires access to a workload cluster
	withWorkloadCluster bool
	// secretNamespace is the namespace to watch secrets in on the platform cluster. Used only if the ServiceProviderReconciler also implements SecretWatcher.
	secretNamespace string
	// emptyObj creates an empty object of the api type
	emptyObj func() T
}

// NewSPReconciler creates a reconciler instance for the given types.
func NewSPReconciler[T ServiceProviderAPI, PC ProviderConfig](emptyObj func() T) *SPReconciler[T, PC] {
	return &SPReconciler[T, PC]{
		emptyObj: emptyObj,
	}
}

// WithPlatformCluster set the platform cluster.
func (r *SPReconciler[T, PC]) WithPlatformCluster(c *clusters.Cluster) *SPReconciler[T, PC] {
	r.platformCluster = c
	return r
}

// WithOnboardingCluster set the onboarding cluster.
func (r *SPReconciler[T, PC]) WithOnboardingCluster(c *clusters.Cluster) *SPReconciler[T, PC] {
	r.onboardingCluster = c
	return r
}

// WithClusterAccessReconciler sets the cluster access reconciler.
func (r *SPReconciler[T, PC]) WithClusterAccessReconciler(car ClusterAccessProvider) *SPReconciler[T, PC] {
	r.clusterAccessReconciler = car
	return r
}

// WithServiceProviderReconciler sets the service provider reconciler.
func (r *SPReconciler[T, PC]) WithServiceProviderReconciler(dsr ServiceProviderReconciler[T, PC]) *SPReconciler[T, PC] {
	r.serviceProviderReconciler = dsr
	return r
}

// WithWorkloadCluster sets if the service provider reconciler requests a workload cluster
func (r *SPReconciler[T, PC]) WithWorkloadCluster(b bool) *SPReconciler[T, PC] {
	r.withWorkloadCluster = b
	return r
}

// WithSecretNamespace enables secret watching in the given namespace on the platform cluster. Only used if the ServiceProviderReconciler also implements SecretWatcher.
func (r *SPReconciler[T, PC]) WithSecretNamespace(ns string) *SPReconciler[T, PC] {
	r.secretNamespace = ns
	return r
}

// WithProviderConfig sets if the service provider config.
func (r *SPReconciler[T, PC]) WithProviderConfig(config PC) *SPReconciler[T, PC] {
	r.providerConfig.Store(&config)
	return r
}

// Reconcile orchestrates platform and DomainServiceReconciler logic to reconcile APIObjects
func (r *SPReconciler[T, PC]) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reconcileErr error) {
	l := logf.FromContext(ctx)
	// common reconciler logic including get obj, providerconfig, mcp/workload access
	obj := r.emptyObj()
	if err := r.onboardingCluster.Client().Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Skip reconciliation if annotation is set
	if obj.GetAnnotations()[apiconst.OperationAnnotation] == apiconst.OperationAnnotationValueIgnore {
		l.Info("Skipping resource due to ignore operation annotation")
		return ctrl.Result{}, nil
	}
	oldObj := obj.DeepCopyObject().(T)
	// always try to update the obj status
	defer func() {
		if err := r.updateStatus(ctx, obj, oldObj); err != nil {
			l.Error(err, "status update failed")
			reconcileErr = errors.Join(reconcileErr, err)
		}
	}()
	providerConfig := r.providerConfig.Load()
	if providerConfig == nil {
		StatusProgressing(obj, reasonReconcileError, "No ProviderConfig found")
		return ctrl.Result{}, errors.New("provider config missing")
	}
	providerConfigCopy := (*providerConfig).DeepCopyObject().(PC)
	// core crud
	deleted := !obj.GetDeletionTimestamp().IsZero()
	var res ctrl.Result
	var err error
	if deleted {
		res, err = r.delete(ctx, obj, providerConfigCopy)
	} else {
		res, err = r.createOrUpdate(ctx, obj, providerConfigCopy)
	}
	// return based on result/err
	if err != nil {
		l.Error(err, "reconcile failed")
		return ctrl.Result{}, err
	}
	if res.RequeueAfter > 0 {
		return res, nil
	}
	// fallback to poll interval to prevent 'managed service' drift
	return ctrl.Result{
		RequeueAfter: providerConfigCopy.PollInterval(),
	}, nil
}

func (r *SPReconciler[T, PC]) updateStatus(ctx context.Context, newObj T, oldObj T) error {
	if equality.Semantic.DeepEqual(oldObj.GetStatus(), newObj.GetStatus()) {
		return nil
	}
	err := r.onboardingCluster.Client().Status().Patch(ctx, newObj, client.MergeFrom(oldObj))
	// can't update status if object doesn't exist
	return client.IgnoreNotFound(err)
}

// delete eventually invokes the domain delete logic of a service provider and is the place to implement
// common logic that should be abstracted away from a service provider developer like handling cluster access.
func (r *SPReconciler[T, PC]) delete(ctx context.Context, obj T, pc PC) (ctrl.Result, error) {
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
	accessRequestsInDeletion, err := r.areAccessRequestsInDeletion(ctx, req)
	if err != nil {
		StatusProgressing(obj, reasonReconcileError, "failed to check access requests in deletion")
		return reconcile.Result{}, err
	}
	if !accessRequestsInDeletion {
		clusters, res, err := r.clusters(ctx, req)
		if err != nil {
			terminatingWithReason(obj, reasonReconcileError, "cluster cleanup error")
			return ctrl.Result{}, err
		}
		if res.RequeueAfter > 0 {
			terminatingWithReason(obj, "Reconciling", "cluster cleanup")
			return res, nil
		}
		res, err = r.serviceProviderReconciler.Delete(ctx, obj, pc, clusters)
		if err != nil {
			return ctrl.Result{}, err
		}
		if res.RequeueAfter > 0 {
			return res, nil
		}
	}
	// remove cluster access
	res, err := r.clusterAccessReconciler.ReconcileDelete(ctx, req)
	if err != nil {
		terminatingWithReason(obj, reasonReconcileError, "failed cluster access reconcile delete")
		return ctrl.Result{}, err
	}
	// make sure to not drop the object before cleanup has been done
	if res.RequeueAfter > 0 {
		return res, nil
	}
	// remove finalizer
	controllerutil.RemoveFinalizer(obj, obj.Finalizer())
	if err := r.onboardingCluster.Client().Update(ctx, obj); err != nil {
		terminatingWithReason(obj, reasonReconcileError, "failed to remove finalizer")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// createOrUpdate eventually invokes the domain createOrUpdate logic of a service provider and is the place to implement
// common logic that should be abstracted away from a service provider developer like handling cluster access.
func (r *SPReconciler[T, PC]) createOrUpdate(ctx context.Context, obj T, pc PC) (ctrl.Result, error) {
	if _, err := controllerutil.CreateOrUpdate(ctx, r.onboardingCluster.Client(), obj, func() error {
		controllerutil.AddFinalizer(obj, obj.Finalizer())
		return nil
	}); err != nil {
		StatusProgressing(obj, reasonReconcileError, "failed to add finalizer")
		return ctrl.Result{}, err
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
	clusters, res, err := r.clusters(ctx, req)
	if err != nil {
		StatusProgressing(obj, reasonReconcileError, "cluster setup error")
		return ctrl.Result{}, err
	}
	if res.RequeueAfter > 0 {
		return res, nil
	}
	return r.serviceProviderReconciler.CreateOrUpdate(ctx, obj, pc, clusters)
}

// areAccessRequestsInDeletion determines if the access requests for a reconcile request are in deletion.
// It returns true if any access requests (mcp, workload) is deleted or has a deletion timestamp.
// It is used to prevent renewing cluster access when deleting an ServiceProviderAPI object.
func (r *SPReconciler[T, PC]) areAccessRequestsInDeletion(ctx context.Context, req ctrl.Request) (bool, error) {
	accessRequest, err := r.clusterAccessReconciler.MCPAccessRequest(ctx, req)
	if apierrors.IsNotFound(err) || (accessRequest != nil && accessRequest.DeletionTimestamp != nil) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if r.withWorkloadCluster {
		accessRequest, err = r.clusterAccessReconciler.WorkloadAccessRequest(ctx, req)
		if apierrors.IsNotFound(err) || (accessRequest != nil && accessRequest.DeletionTimestamp != nil) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
	}
	return false, nil
}

// clusters returns any request scoped cluster that a servicer provider developer might want to access in order
// to delivery its service.
func (r *SPReconciler[T, PC]) clusters(ctx context.Context, req ctrl.Request) (ClusterContext, ctrl.Result, error) {
	clusters := ClusterContext{}
	res, err := r.clusterAccessReconciler.Reconcile(ctx, req)
	if err != nil {
		return clusters, ctrl.Result{}, err
	}
	if res.RequeueAfter > 0 {
		return clusters, res, nil
	}
	mcpCluster, err := r.clusterAccessReconciler.MCPCluster(ctx, req)
	if err != nil {
		return clusters, ctrl.Result{}, err
	}
	if mcpCluster == nil {
		return clusters, res, errors.New("mcp access missing")
	}
	clusters.MCPCluster = mcpCluster
	ar, err := r.clusterAccessReconciler.MCPAccessRequest(ctx, req)
	if err != nil {
		return clusters, ctrl.Result{}, err
	}
	clusters.MCPAccessSecretKey = retrieveSecretKey(ar)
	if r.withWorkloadCluster {
		workloadCluster, err := r.clusterAccessReconciler.WorkloadCluster(ctx, req)
		if err != nil {
			return clusters, ctrl.Result{}, err
		}
		if workloadCluster == nil {
			return clusters, res, errors.New("workload cluster access missing")
		}
		clusters.WorkloadCluster = workloadCluster
		ar, err := r.clusterAccessReconciler.WorkloadAccessRequest(ctx, req)
		if err != nil {
			return clusters, ctrl.Result{}, err
		}
		clusters.WorkloadAccessSecretKey = retrieveSecretKey(ar)
	}
	return clusters, res, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SPReconciler[T, PC]) SetupWithManager(mgr ctrl.Manager, name string, providerConfigUpdates chan event.GenericEvent) error {
	controller := ctrl.NewControllerManagedBy(mgr).
		For(r.emptyObj()).
		// sets up reconciles whenever provider config controller sends update events
		WatchesRawSource(
			source.Channel(
				providerConfigUpdates,
				handler.EnqueueRequestsFromMapFunc(
					func(ctx context.Context, obj client.Object) []reconcile.Request {
						// update cached provider config
						if obj != nil {
							c := obj.DeepCopyObject().(PC)
							r.providerConfig.Store(&c)
						} else {
							r.providerConfig.Store(nil)
						}
						// reconcile all existing objects
						return r.enqueueAllObjects(ctx)
					},
				)),
		)

	// Optional: watch secrets on the platform cluster if the reconciler implements SecretWatcher
	if sw, ok := r.serviceProviderReconciler.(SecretWatcher[PC]); ok && r.secretNamespace != "" {
		controller = controller.WatchesRawSource(
			source.Kind(
				r.platformCluster.Cluster().GetCache(),
				&corev1.Secret{},
				handler.TypedEnqueueRequestsFromMapFunc(r.mapSecretToRequests(sw)),
				controllerutil2.ToTypedPredicate[*corev1.Secret](
					predicate.NewPredicateFuncs(func(obj client.Object) bool {
						return obj.GetNamespace() == r.secretNamespace
					}),
				),
			),
		)
	}

	return controller.Named(name).Complete(r)
}

// mapSecretToRequests returns a typed map function that checks whether a changed secret
// is referenced by the service provider and, if so, enqueues all ServiceProviderAPI objects.
func (r *SPReconciler[T, PC]) mapSecretToRequests(sw SecretWatcher[PC]) func(ctx context.Context, secret *corev1.Secret) []reconcile.Request {
	return func(ctx context.Context, secret *corev1.Secret) []reconcile.Request {
		var pcVal PC
		if pc := r.providerConfig.Load(); pc != nil {
			pcVal = *pc
		}
		if !sw.IsReferencedSecret(ctx, secret, pcVal) {
			return nil
		}
		return r.enqueueAllObjects(ctx)
	}
}

// enqueueAllObjects lists all ServiceProviderAPI objects and returns a reconcile request for each.
func (r *SPReconciler[T, PC]) enqueueAllObjects(ctx context.Context) []reconcile.Request {
	var list unstructured.UnstructuredList
	gvk, err := apiutil.GVKForObject(r.emptyObj(), r.onboardingCluster.Scheme())
	if err != nil {
		logf.FromContext(ctx).Error(err, "failed to retrieve gvk")
		return nil
	}
	list.SetGroupVersionKind(gvk)
	if err := r.onboardingCluster.Client().List(ctx, &list); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list objects")
		return nil
	}
	reqs := make([]reconcile.Request, len(list.Items))
	for i := range list.Items {
		reqs[i] = reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(&list.Items[i]),
		}
	}
	return reqs
}

func retrieveSecretKey(ar *clustersv1alpha1.AccessRequest) client.ObjectKey {
	if ar.Status.SecretRef == nil {
		return client.ObjectKey{}
	}
	return client.ObjectKey{
		Namespace: ar.Namespace,
		Name:      ar.Status.SecretRef.Name,
	}
}
