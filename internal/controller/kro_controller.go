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
	"fmt"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/openmcp-project/controller-utils/pkg/clusters"
	ctrlerrors "github.com/openmcp-project/controller-utils/pkg/errors"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openmcp-project/opencontrolplane-runtime/pkg/serviceprovider"
	"github.com/openmcp-project/opencontrolplane-runtime/pkg/serviceprovider/clusteraccess"

	apiv1alpha1 "github.com/openmcp-project/service-provider-kro/api/v1alpha1"
)

const (
	// HelmReleaseName is the name of the helmRelease object created for the controller.
	HelmReleaseName = "kro-helm-release"
	// OCIRepositoryName is the name of the oci repository object pointing to the helm chart of the controller.
	OCIRepositoryName = "kro-oci-repository"
	// KroSystemNamespace is the default namespace on the target cluster to use to install the Kro controller into.
	KroSystemNamespace = "kro-system"

	// kroAPIGroup is the API group under which kro serves its custom resources.
	kroAPIGroup = "kro.run"
	// kroAPIVersion is the API version of kro's ResourceGraphDefinition resource.
	kroAPIVersion = "v1alpha1"
	// resourceGraphDefinitionListKind is the list kind of kro's ResourceGraphDefinition resource.
	resourceGraphDefinitionListKind = "ResourceGraphDefinitionList"
	// deletionBlockedRequeue is how long to wait before re-checking whether the
	// user's kro resources have been removed and deletion may proceed.
	deletionBlockedRequeue = 10 * time.Second

	// conditionReasonError is the Ready condition reason used when a reconcile step fails.
	conditionReasonError = "ReconcileError"
)

// ClusterAccessName is the name of the access object containing the kubeconfig for the mcp target cluster.
var ClusterAccessName = apiv1alpha1.GroupVersion.Group

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
func (r *KroReconciler) CreateOrUpdate(ctx context.Context, svcobj *apiv1alpha1.Kro, providerConfig *apiv1alpha1.ProviderConfig, clusterCtx clusteraccess.ClusterContext) (ctrl.Result, error) {
	l := logf.FromContext(ctx)
	l.Info("Reconciling Kro resource", "name", svcobj.Name)
	serviceprovider.StatusProgressing(svcobj, "Reconciling", "Reconcile in progress")
	tenantNamespace, err := libutils.StableMCPNamespace(svcobj.Name, svcobj.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine stable namespace for Kro instance: %w", err)
	}

	l.Info("checking tenantNamespace", "namespace", tenantNamespace)

	// Map the tenant requested version onto the artifacts the ProviderConfig offers for it.
	version, err := providerConfig.ResolveVersion(svcobj.Spec.Version)
	if err != nil {
		l.Info("requested version is not offered by the provider config", "version", svcobj.Spec.Version, "error", err.Error())
		serviceprovider.StatusProgressing(svcobj, conditionReasonError, err.Error())
		return ctrl.Result{}, ctrlerrors.IgnoreInvalidUserInput(err)
	}
	l.Info("resolved requested version", "version", version.Version, "chartVersion", version.ChartVersion, "chartURL", version.GetChartURL())

	if err := r.replicateChartPullSecret(ctx, version.ChartPullSecret, tenantNamespace); err != nil {
		serviceprovider.StatusProgressing(svcobj, conditionReasonError, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to replicate chart pull secret: %w", err)
	}

	ociRepo, err := r.createOrUpdateOCIRepository(ctx, version, tenantNamespace)
	if err != nil {
		serviceprovider.StatusProgressing(svcobj, conditionReasonError, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to reconcile OCI Repository: %w", err)
	}
	if err := r.replicateMCPImagePullSecrets(ctx, clusterCtx.MCPCluster, version.HelmValues); err != nil {
		serviceprovider.StatusProgressing(svcobj, conditionReasonError, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to replicate MCP image pull secrets: %w", err)
	}
	helmRel, err := r.createOrUpdateHelmRelease(ctx, tenantNamespace, version.HelmValues, clusterCtx)
	if err != nil {
		serviceprovider.StatusProgressing(svcobj, conditionReasonError, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to reconcile HelmRelease: %w", err)
	}

	l.Info("Done reconciling Kro resource", "name", svcobj.Name)

	ociPhase, ociMsg := resourceStatus(ociRepo.Status.Conditions)
	hrPhase, hrMsg := resourceStatus(helmRel.Status.Conditions)
	svcobj.Status.Resources = []apiv1alpha1.ManagedResource{
		{
			TypedObjectReference: corev1.TypedObjectReference{
				APIGroup:  new(sourcev1.GroupVersion.Group),
				Kind:      "OCIRepository",
				Name:      OCIRepositoryName,
				Namespace: new(tenantNamespace),
			},
			Phase:    ociPhase,
			Message:  ociMsg,
			Location: apiv1alpha1.PlatformCluster,
		},
		{
			TypedObjectReference: corev1.TypedObjectReference{
				APIGroup:  new(helmv2.GroupVersion.Group),
				Kind:      "HelmRelease",
				Name:      HelmReleaseName,
				Namespace: new(tenantNamespace),
			},
			Phase:    hrPhase,
			Message:  hrMsg,
			Location: apiv1alpha1.PlatformCluster,
		},
	}

	if ociPhase == apiv1alpha1.Ready && hrPhase == apiv1alpha1.Ready {
		serviceprovider.StatusReady(svcobj)
	} else {
		serviceprovider.StatusProgressing(svcobj, "Reconciling", "Waiting for managed resources to become ready")
	}
	// The SPReconciler wrapper applies PollInterval as a fallback RequeueAfter.
	return ctrl.Result{}, nil
}

// Delete is called on every delete event.
func (r *KroReconciler) Delete(ctx context.Context, obj *apiv1alpha1.Kro, _ *apiv1alpha1.ProviderConfig, clusterCtx clusteraccess.ClusterContext) (ctrl.Result, error) {
	l := logf.FromContext(ctx)
	serviceprovider.StatusTerminating(obj)

	tenantNamespace, err := libutils.StableMCPNamespace(obj.Name, obj.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine stable namespace for Kro instance: %w", err)
	}

	// Block deletion until the user has no more Kro objects left.
	if clusterCtx.MCPCluster != nil {
		remaining, err := r.countResourceGraphDefinitions(ctx, clusterCtx.MCPCluster)
		if err != nil {
			serviceprovider.StatusTerminatingWithReason(obj, conditionReasonError, err.Error())
			return ctrl.Result{}, fmt.Errorf("failed to check for remaining ResourceGraphDefinitions: %w", err)
		}
		if remaining > 0 {
			msg := fmt.Sprintf("deletion blocked: waiting for %d kro ResourceGraphDefinition(s) to be removed from the control plane", remaining)
			l.Info(msg)
			serviceprovider.StatusTerminatingWithReason(obj, "ResourcesRemain", msg)
			obj.Status.Resources = managedResources(tenantNamespace, apiv1alpha1.Terminating)
			return ctrl.Result{RequeueAfter: deletionBlockedRequeue}, nil
		}
	}

	obj.Status.Resources = managedResources(tenantNamespace, apiv1alpha1.Terminating)

	objects := []client.Object{
		&sourcev1.OCIRepository{
			ObjectMeta: metav1.ObjectMeta{Name: OCIRepositoryName, Namespace: tenantNamespace},
		},
		&helmv2.HelmRelease{
			ObjectMeta: metav1.ObjectMeta{Name: HelmReleaseName, Namespace: tenantNamespace},
		},
	}

	objectsStillExist := false
	for _, managedObj := range objects {
		if err := r.PlatformCluster.Client().Delete(ctx, managedObj); client.IgnoreNotFound(err) != nil {
			serviceprovider.StatusTerminatingWithReason(obj, conditionReasonError, err.Error())
			return ctrl.Result{}, fmt.Errorf("delete object failed: %w", err)
		}
		// Only a NotFound confirms the object is gone. A successful Get means it is still
		// there, and any other error leaves us unsure, so re-check on the requeue.
		if err := r.PlatformCluster.Client().Get(ctx, client.ObjectKeyFromObject(managedObj), managedObj); !apierrors.IsNotFound(err) {
			objectsStillExist = true
		}
	}

	if objectsStillExist {
		return ctrl.Result{
			RequeueAfter: time.Second * 10,
		}, nil
	}

	obj.Status.Resources = nil
	serviceprovider.StatusReady(obj)
	return ctrl.Result{}, nil
}

// countResourceGraphDefinitions returns the number of kro ResourceGraphDefinition
// objects present on the given (control plane) cluster. If the kind is not installed
// this just returns 0.
func (r *KroReconciler) countResourceGraphDefinitions(ctx context.Context, mcpCluster *clusters.Cluster) (int, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   kroAPIGroup,
		Version: kroAPIVersion,
		Kind:    resourceGraphDefinitionListKind,
	})
	if err := mcpCluster.Client().List(ctx, list); err != nil {
		if apimeta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			return 0, nil
		}
		return 0, err
	}
	return len(list.Items), nil
}

// managedResources returns the set of platform-cluster objects this controller
// owns for a Kro instance, tagged with the given lifecycle phase.
func managedResources(tenantNamespace string, phase apiv1alpha1.InstancePhase) []apiv1alpha1.ManagedResource {
	return []apiv1alpha1.ManagedResource{
		{
			TypedObjectReference: corev1.TypedObjectReference{
				APIGroup:  new(sourcev1.GroupVersion.Group),
				Kind:      "OCIRepository",
				Name:      OCIRepositoryName,
				Namespace: new(tenantNamespace),
			},
			Phase:    phase,
			Location: apiv1alpha1.PlatformCluster,
		},
		{
			TypedObjectReference: corev1.TypedObjectReference{
				APIGroup:  new(helmv2.GroupVersion.Group),
				Kind:      "HelmRelease",
				Name:      HelmReleaseName,
				Namespace: new(tenantNamespace),
			},
			Phase:    phase,
			Location: apiv1alpha1.PlatformCluster,
		},
	}
}

// resourceStatus maps a Flux resource's Ready condition to an InstancePhase.
// Returns Ready with an empty message when ready, otherwise Progressing with
// the Ready condition's message (or empty if the condition is absent).
func resourceStatus(conditions []metav1.Condition) (apiv1alpha1.InstancePhase, string) {
	if apimeta.IsStatusConditionTrue(conditions, meta.ReadyCondition) {
		return apiv1alpha1.Ready, ""
	}
	if cond := apimeta.FindStatusCondition(conditions, meta.ReadyCondition); cond != nil {
		return apiv1alpha1.Progressing, cond.Message
	}
	return apiv1alpha1.Progressing, ""
}

// replicateChartPullSecret copies the named secret from the controller's namespace
// (r.PodNamespace) on the platform cluster into targetNamespace, where the OCIRepository
// references it. An empty secret name is a no-op.
func (r *KroReconciler) replicateChartPullSecret(ctx context.Context, secretName, targetNamespace string) error {
	if secretName == "" {
		return nil
	}
	platformClient := r.PlatformCluster.Client()

	sourceSecret := &corev1.Secret{}
	sourceKey := client.ObjectKey{Name: secretName, Namespace: r.PodNamespace}
	if err := platformClient.Get(ctx, sourceKey, sourceSecret); err != nil {
		return fmt.Errorf("failed to get secret %q from namespace %q: %w", secretName, r.PodNamespace, err)
	}

	targetSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: targetNamespace,
		},
	}
	if _, err := ctrl.CreateOrUpdate(ctx, platformClient, targetSecret, func() error {
		targetSecret.Data = sourceSecret.Data
		targetSecret.Type = sourceSecret.Type
		return nil
	}); err != nil {
		return fmt.Errorf("failed to replicate secret %q to namespace %q: %w", secretName, targetNamespace, err)
	}

	return nil
}

// replicateMCPImagePullSecrets copies every secret referenced under
// `imagePullSecrets` in the version's Helm values from the controller's
// own namespace on the platform cluster into the kro-system namespace on the
// MCP cluster, so the deployed controller can pull its images from private
// registries. The target namespace is created if it does not exist.
//
// Cleanup is not required: when the MCP is torn down or the chart namespace is
// removed, the copied secrets are garbage-collected with it.
func (r *KroReconciler) replicateMCPImagePullSecrets(ctx context.Context, mcpCluster *clusters.Cluster, values *apiextensionsv1.JSON) error {
	helmValues, err := ExtractHelmValues(values)
	if err != nil {
		return err
	}
	if len(helmValues.ImagePullSecrets) == 0 {
		return nil
	}
	if mcpCluster == nil {
		return fmt.Errorf("mcp cluster is required to replicate image pull secrets but was nil")
	}

	platformClient := r.PlatformCluster.Client()
	mcpClient := mcpCluster.Client()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: KroSystemNamespace}}
	if _, err := ctrl.CreateOrUpdate(ctx, mcpClient, ns, func() error { return nil }); err != nil {
		return fmt.Errorf("failed to ensure namespace %q on mcp cluster: %w", KroSystemNamespace, err)
	}

	for _, ref := range helmValues.ImagePullSecrets {
		source := &corev1.Secret{}
		sourceKey := client.ObjectKey{Name: ref.Name, Namespace: r.PodNamespace}
		if err := platformClient.Get(ctx, sourceKey, source); err != nil {
			return fmt.Errorf("failed to get image pull secret %q from namespace %q: %w", ref.Name, r.PodNamespace, err)
		}
		target := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ref.Name,
				Namespace: KroSystemNamespace,
			},
		}
		if _, err := ctrl.CreateOrUpdate(ctx, mcpClient, target, func() error {
			target.Data = source.Data
			target.Type = source.Type
			return nil
		}); err != nil {
			return fmt.Errorf("failed to replicate image pull secret %q to mcp namespace %q: %w", ref.Name, KroSystemNamespace, err)
		}
	}
	return nil
}

func (r *KroReconciler) createOrUpdateOCIRepository(ctx context.Context, version apiv1alpha1.KroVersion, namespace string) (*sourcev1.OCIRepository, error) {
	ociRepository := createOciRepository(version, namespace)
	managedObj := &sourcev1.OCIRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ociRepository.Name,
			Namespace: namespace,
		},
	}
	l := logf.FromContext(ctx)
	l.Info("creating OCI Repository", "object", ociRepository)
	if _, err := ctrl.CreateOrUpdate(ctx, r.PlatformCluster.Client(), managedObj, func() error {
		managedObj.Spec = ociRepository.Spec
		return nil
	}); err != nil {
		return nil, err
	}

	return managedObj, nil
}

func (r *KroReconciler) createOrUpdateHelmRelease(ctx context.Context, namespace string, values *apiextensionsv1.JSON, clusterCtx clusteraccess.ClusterContext) (*helmv2.HelmRelease, error) {
	helmRelease, err := r.createHelmRelease(namespace, values, clusterCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to create helm release: %w", err)
	}
	managedObj := &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      helmRelease.Name,
			Namespace: namespace,
		},
	}
	l := logf.FromContext(ctx)
	l.Info("creating Helm Release", "object", managedObj)
	if _, err := ctrl.CreateOrUpdate(ctx, r.PlatformCluster.Client(), managedObj, func() error {
		managedObj.Spec = helmRelease.Spec
		return nil
	}); err != nil {
		return nil, err
	}

	return managedObj, nil
}

func createOciRepository(version apiv1alpha1.KroVersion, namespace string) *sourcev1.OCIRepository {
	var secretRef *meta.LocalObjectReference
	if version.ChartPullSecret != "" {
		secretRef = &meta.LocalObjectReference{Name: version.ChartPullSecret}
	}

	return &sourcev1.OCIRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      OCIRepositoryName,
			Namespace: namespace,
		},
		Spec: sourcev1.OCIRepositorySpec{
			Interval:  metav1.Duration{Duration: time.Minute},
			URL:       version.GetChartURL(),
			SecretRef: secretRef,
			Reference: &sourcev1.OCIRepositoryRef{
				Tag: version.ChartVersion,
			},
		},
	}
}

func (r *KroReconciler) createHelmRelease(namespace string, helmValues *apiextensionsv1.JSON, clusterCtx clusteraccess.ClusterContext) (*helmv2.HelmRelease, error) {
	return &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      HelmReleaseName,
			Namespace: namespace,
		},
		Spec: helmv2.HelmReleaseSpec{
			ReleaseName:      apiv1alpha1.DefaultReleaseName,
			Interval:         metav1.Duration{Duration: time.Minute},
			TargetNamespace:  KroSystemNamespace,
			StorageNamespace: KroSystemNamespace,
			Install: &helmv2.Install{
				CRDs:            helmv2.Create,
				CreateNamespace: true,
				Remediation: &helmv2.InstallRemediation{
					Retries: 3,
				},
			},
			Upgrade: &helmv2.Upgrade{
				CRDs:          helmv2.CreateReplace,
				CleanupOnFail: true,
				Remediation: &helmv2.UpgradeRemediation{
					Retries:  3,
					Strategy: new(helmv2.RollbackRemediationStrategy),
				},
			},
			ChartRef: &helmv2.CrossNamespaceSourceReference{
				Kind:      "OCIRepository",
				Name:      OCIRepositoryName,
				Namespace: namespace,
			},
			Values: helmValues,
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: &meta.SecretKeyReference{
					Name: clusterCtx.MCPAccessSecretKey.Name,
					Key:  "kubeconfig",
				},
			},
		},
	}, nil
}
