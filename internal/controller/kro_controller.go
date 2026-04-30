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
	"strings"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/openmcp-project/controller-utils/pkg/clusters"
	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	"github.com/openmcp-project/openmcp-operator/lib/clusteraccess"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	apiv1alpha1 "github.com/openmcp-project/service-provider-kro/api/v1alpha1"
	"github.com/openmcp-project/service-provider-kro/pkg/spruntime"
)

const (
	// HelmReleaseName is the name of the helmRelease object created for the controller.
	HelmReleaseName = "kro-helm-release"
	// OCIRepositoryName is the name of the oci repository object pointing to the helm chart of the controller.
	OCIRepositoryName = "kro-oci-repository"
	// KroSystemNamespace is the default namespace on the target cluster to use to install the Kro controller into.
	KroSystemNamespace = "kro-system"
	// requestSuffixMCP is the suffix used for the mcp cluster.
	requestSuffixMCP = "--mcp"
)

// clusterAccessName is the name of the access object containing the kubeconfig for the mcp target cluster.
var clusterAccessName = apiv1alpha1.GroupVersion.Group

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
func (r *KroReconciler) CreateOrUpdate(ctx context.Context, svcobj *apiv1alpha1.Kro, providerConfig *apiv1alpha1.ProviderConfig, clusterCtx spruntime.ClusterContext) (ctrl.Result, error) {
	l := logf.FromContext(ctx)
	l.Info("Reconciling Kro resource", "name", svcobj.Name)
	spruntime.StatusProgressing(svcobj, "Reconciling", "Reconcile in progress")
	tenantNamespace, err := libutils.StableMCPNamespace(svcobj.Name, svcobj.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine stable namespace for Kro instance: %w", err)
	}

	l.Info("checking tenantNamespace", "namespace", tenantNamespace)

	if err := r.replicateImagePullSecret(ctx, providerConfig, tenantNamespace); err != nil {
		spruntime.StatusProgressing(svcobj, spruntime.StatusPhaseFailed, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to replicate image pull secret: %w", err)
	}

	if err := r.createOrUpdateOCIRepository(ctx, svcobj, clusterCtx, tenantNamespace, providerConfig); err != nil {
		spruntime.StatusProgressing(svcobj, spruntime.StatusPhaseFailed, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to reconcile OCI Repository: %w", err)
	}
	if err := r.replicateMCPImagePullSecrets(ctx, clusterCtx.MCPCluster, providerConfig); err != nil {
		spruntime.StatusProgressing(svcobj, spruntime.StatusPhaseFailed, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to replicate MCP image pull secrets: %w", err)
	}
	if err := r.createOrUpdateHelmRelease(ctx, tenantNamespace, svcobj, providerConfig); err != nil {
		spruntime.StatusProgressing(svcobj, spruntime.StatusPhaseFailed, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to reconcile HelmRelease: %w", err)
	}

	l.Info("Done reconciling Kro resource", "name", svcobj.Name)

	svcobj.Status.Resources = managedResources(tenantNamespace, apiv1alpha1.Ready)
	spruntime.StatusReady(svcobj)
	return ctrl.Result{}, nil
}

// Delete is called on every delete event
func (r *KroReconciler) Delete(ctx context.Context, obj *apiv1alpha1.Kro, providerConfig *apiv1alpha1.ProviderConfig, _ spruntime.ClusterContext) (ctrl.Result, error) {
	spruntime.StatusTerminating(obj)

	tenantNamespace, err := libutils.StableMCPNamespace(obj.Name, obj.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine stable namespace for Kro instance: %w", err)
	}

	obj.Status.Resources = managedResources(tenantNamespace, apiv1alpha1.Terminating)

	var objects []client.Object
	ociRepository := createOciRepository(providerConfig, obj.Spec.Version, tenantNamespace)
	objects = append(objects, ociRepository)
	helmRelease, err := r.createHelmRelease(ctx, tenantNamespace, obj, providerConfig)
	if err != nil {
		spruntime.StatusProgressing(obj, spruntime.StatusPhaseFailed, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to create helm release: %w", err)
	}
	objects = append(objects, helmRelease)

	objectsStillExist := false
	for _, managedObj := range objects {
		if err := r.PlatformCluster.Client().Delete(ctx, managedObj); client.IgnoreNotFound(err) != nil {
			spruntime.StatusProgressing(obj, spruntime.StatusPhaseFailed, err.Error())
			return ctrl.Result{}, fmt.Errorf("delete object failed: %w", err)
		}
		// we ignore any other error because we assume that if deleting worked, getting should not fail with anything other than
		// not found.
		if err := r.PlatformCluster.Client().Get(ctx, client.ObjectKeyFromObject(managedObj), managedObj); err != nil {
			objectsStillExist = true
		}
	}

	if objectsStillExist {
		return ctrl.Result{
			RequeueAfter: time.Second * 10,
		}, nil
	}

	obj.Status.Resources = nil
	spruntime.StatusReady(obj)
	return ctrl.Result{}, nil
}

// managedResources returns the set of platform-cluster objects this controller
// owns for a Kro instance, tagged with the given lifecycle phase.
func managedResources(tenantNamespace string, phase apiv1alpha1.InstancePhase) []apiv1alpha1.ManagedResource {
	ns := tenantNamespace
	ociGroup := sourcev1.GroupVersion.Group
	helmGroup := helmv2.GroupVersion.Group
	return []apiv1alpha1.ManagedResource{
		{
			TypedObjectReference: corev1.TypedObjectReference{
				APIGroup:  &ociGroup,
				Kind:      "OCIRepository",
				Name:      OCIRepositoryName,
				Namespace: &ns,
			},
			Phase:    phase,
			Location: apiv1alpha1.PlatformCluster,
		},
		{
			TypedObjectReference: corev1.TypedObjectReference{
				APIGroup:  &helmGroup,
				Kind:      "HelmRelease",
				Name:      HelmReleaseName,
				Namespace: &ns,
			},
			Phase:    phase,
			Location: apiv1alpha1.PlatformCluster,
		},
	}
}

func (r *KroReconciler) getMcpFluxConfig(ctx context.Context, namespace, objectName string) (*meta.SecretKeyReference, error) {
	mcpAccessRequest := &clustersv1alpha1.AccessRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusteraccess.StableRequestNameFromLocalName(clusterAccessName, objectName) + requestSuffixMCP,
			Namespace: namespace,
		},
	}

	if err := r.PlatformCluster.Client().Get(ctx, client.ObjectKeyFromObject(mcpAccessRequest), mcpAccessRequest); err != nil {
		return nil, fmt.Errorf("failed to get MCP AccessRequest: %w", err)
	}

	return &meta.SecretKeyReference{
		Name: mcpAccessRequest.Status.SecretRef.Name,
		Key:  "kubeconfig",
	}, nil
}

func (r *KroReconciler) replicateImagePullSecret(ctx context.Context, providerConfig *apiv1alpha1.ProviderConfig, targetNamespace string) error {
	ref := providerConfig.GetImagePullSecret()
	if ref == nil {
		return nil
	}
	platformClient := r.PlatformCluster.Client()

	sourceSecret := &corev1.Secret{}
	sourceKey := client.ObjectKey{Name: ref.Name, Namespace: r.PodNamespace}
	if err := platformClient.Get(ctx, sourceKey, sourceSecret); err != nil {
		return fmt.Errorf("failed to get image pull secret %q from namespace %q: %w", ref.Name, r.PodNamespace, err)
	}

	targetSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ref.Name,
			Namespace: targetNamespace,
		},
	}
	if _, err := ctrl.CreateOrUpdate(ctx, platformClient, targetSecret, func() error {
		targetSecret.Data = sourceSecret.Data
		targetSecret.Type = sourceSecret.Type
		return nil
	}); err != nil {
		return fmt.Errorf("failed to replicate image pull secret %q to namespace %q: %w", ref.Name, targetNamespace, err)
	}

	return nil
}

// replicateMCPImagePullSecrets copies every secret referenced under
// `imagePullSecrets` in the ProviderConfig Helm values from the controller's
// own namespace on the platform cluster into the kro-system namespace on the
// MCP cluster, so the deployed controller can pull its images from private
// registries. The target namespace is created if it does not exist.
//
// Cleanup is not required: when the MCP is torn down or the chart namespace is
// removed, the copied secrets are garbage-collected with it.
func (r *KroReconciler) replicateMCPImagePullSecrets(ctx context.Context, mcpCluster *clusters.Cluster, providerConfig *apiv1alpha1.ProviderConfig) error {
	helmValues, err := ExtractHelmValues(providerConfig.GetValues())
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

func (r *KroReconciler) createOrUpdateOCIRepository(ctx context.Context, svcobj *apiv1alpha1.Kro, _ spruntime.ClusterContext, namespace string, providerConfig *apiv1alpha1.ProviderConfig) error {
	ociRepository := createOciRepository(providerConfig, svcobj.Spec.Version, namespace)
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
		return err
	}

	return nil
}

func (r *KroReconciler) createOrUpdateHelmRelease(ctx context.Context, namespace string, svcobj *apiv1alpha1.Kro, providerConfig *apiv1alpha1.ProviderConfig) error {
	helmRelease, err := r.createHelmRelease(ctx, namespace, svcobj, providerConfig)
	if err != nil {
		return fmt.Errorf("failed to create helm release: %w", err)
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
		return err
	}

	return nil
}

func ensureOCIScheme(url *string) string {
	if !strings.HasPrefix(*url, "oci://") {
		return "oci://" + *url
	}
	return *url
}

func createOciRepository(providerConfig *apiv1alpha1.ProviderConfig, version, namespace string) *sourcev1.OCIRepository {
	var secretRef *meta.LocalObjectReference
	if ref := providerConfig.GetImagePullSecret(); ref != nil {
		secretRef = &meta.LocalObjectReference{Name: ref.Name}
	}

	return &sourcev1.OCIRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      OCIRepositoryName,
			Namespace: namespace,
		},
		Spec: sourcev1.OCIRepositorySpec{
			Interval:  metav1.Duration{Duration: time.Minute},
			URL:       ensureOCIScheme(providerConfig.GetChartURL()),
			SecretRef: secretRef,
			Reference: &sourcev1.OCIRepositoryRef{
				Tag: version,
			},
		},
	}
}

func (r *KroReconciler) createHelmRelease(ctx context.Context, namespace string, svcobj *apiv1alpha1.Kro, providerConfig *apiv1alpha1.ProviderConfig) (*helmv2.HelmRelease, error) {
	fluxConfigRef, err := r.getMcpFluxConfig(ctx, namespace, svcobj.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get FluxConfig: %w", err)
	}

	helmValues := providerConfig.GetValues()

	remediationStrategy := helmv2.RollbackRemediationStrategy

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
					Strategy: &remediationStrategy,
				},
			},
			ChartRef: &helmv2.CrossNamespaceSourceReference{
				Kind:      "OCIRepository",
				Name:      OCIRepositoryName,
				Namespace: namespace,
			},
			Values: helmValues,
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: fluxConfigRef,
			},
		},
	}, nil
}
