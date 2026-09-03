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
	"errors"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/openmcp-project/controller-utils/pkg/clusters"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	spruntime "github.com/openmcp-project/opencontrolplane-runtime/pkg/serviceprovider"
	"github.com/openmcp-project/opencontrolplane-runtime/pkg/serviceprovider/clusteraccess"

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

func TestCreateOciRepository_SecretRefs(t *testing.T) {
	tests := []struct {
		name            string
		chartPullSecret string
		wantSecretRef   string
	}{
		{
			name:          "no secret configured",
			wantSecretRef: "",
		},
		{
			name:            "chart pull secret wires secretRef",
			chartPullSecret: "my-pull",
			wantSecretRef:   "my-pull",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			version := apiv1alpha1.KroVersion{
				Version:         "v1.2.3",
				ChartVersion:    "1.2.3",
				ChartURL:        ptr.To("oci://registry.k8s.io/kro/charts/kro"),
				ChartPullSecret: tc.chartPullSecret,
			}
			repo := createOciRepository(version, "tenant")

			// The chart tag comes from ChartVersion, not from the tenant facing Version.
			assert.Equal(t, "1.2.3", repo.Spec.Reference.Tag)

			if tc.wantSecretRef == "" {
				assert.Nil(t, repo.Spec.SecretRef)
			} else {
				require.NotNil(t, repo.Spec.SecretRef)
				assert.Equal(t, tc.wantSecretRef, repo.Spec.SecretRef.Name)
			}
		})
	}
}

func TestCreateOrUpdate_UnknownVersion(t *testing.T) {
	obj := &apiv1alpha1.Kro{
		ObjectMeta: metav1.ObjectMeta{Name: "mcp-01", Namespace: "tenant"},
		Spec:       apiv1alpha1.KroSpec{Version: "9.9.9"},
	}
	pc := &apiv1alpha1.ProviderConfig{
		Spec: apiv1alpha1.ProviderConfigSpec{
			Versions: []apiv1alpha1.KroVersion{{Version: "0.9.3", ChartVersion: "0.9.3"}},
		},
	}
	r := &KroReconciler{}

	res, err := r.CreateOrUpdate(context.Background(), obj, pc, clusteraccess.ClusterContext{})
	require.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)
	assert.Equal(t, spruntime.StatusPhaseProgressing, obj.Status.Phase)

	// The phase cannot express this, so the Ready condition has to carry it: not ready,
	// reason ReconcileError, and a message naming the versions on offer.
	cond := apimeta.FindStatusCondition(obj.Status.Conditions, spruntime.ServiceProviderConditionReady)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, conditionReasonError, cond.Reason)
	assert.Contains(t, cond.Message, `"9.9.9"`)
	assert.Contains(t, cond.Message, "available versions are: 0.9.3")
}

func TestDelete_VersionRemovedFromProviderConfig(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, sourcev1.AddToScheme(scheme))
	require.NoError(t, helmv2.AddToScheme(scheme))

	tenantNamespace, err := libutils.StableMCPNamespace("mcp-01", "tenant")
	require.NoError(t, err)

	platformClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			&sourcev1.OCIRepository{ObjectMeta: metav1.ObjectMeta{Name: OCIRepositoryName, Namespace: tenantNamespace}},
			&helmv2.HelmRelease{ObjectMeta: metav1.ObjectMeta{Name: HelmReleaseName, Namespace: tenantNamespace}},
		).
		Build()

	obj := &apiv1alpha1.Kro{
		ObjectMeta: metav1.ObjectMeta{Name: "mcp-01", Namespace: "tenant"},
		Spec:       apiv1alpha1.KroSpec{Version: "0.9.3"},
	}
	r := &KroReconciler{PlatformCluster: clusters.NewTestClusterFromClient("platform", platformClient)}
	clusterCtx := clusteraccess.ClusterContext{MCPCluster: fakeMCPCluster(t, 0, nil)}

	res, err := r.Delete(context.Background(), obj, &apiv1alpha1.ProviderConfig{}, clusterCtx)
	require.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)
	assert.Nil(t, obj.Status.Resources)

	for _, key := range []client.ObjectKey{
		{Name: OCIRepositoryName, Namespace: tenantNamespace},
		{Name: HelmReleaseName, Namespace: tenantNamespace},
	} {
		err := platformClient.Get(context.Background(), key, &unstructured.Unstructured{})
		assert.Error(t, err, "object %q should be gone", key)
	}
}

func TestDelete_RequeuesWhileObjectsRemain(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, sourcev1.AddToScheme(scheme))
	require.NoError(t, helmv2.AddToScheme(scheme))

	platformClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				return nil
			},
			// The object accepted the delete but is held by a finalizer, so it is still there.
			Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
				return nil
			},
		}).
		Build()

	obj := &apiv1alpha1.Kro{ObjectMeta: metav1.ObjectMeta{Name: "mcp-01", Namespace: "tenant"}}
	r := &KroReconciler{PlatformCluster: clusters.NewTestClusterFromClient("platform", platformClient)}
	clusterCtx := clusteraccess.ClusterContext{MCPCluster: fakeMCPCluster(t, 0, nil)}

	res, err := r.Delete(context.Background(), obj, &apiv1alpha1.ProviderConfig{}, clusterCtx)
	require.NoError(t, err)
	assert.Positive(t, res.RequeueAfter)
	assert.NotNil(t, obj.Status.Resources)
}

// fakeMCPCluster builds a cluster whose client answers a ResourceGraphDefinition list
// with the given behaviour: rgdCount items, or listErr if non-nil.
func fakeMCPCluster(t *testing.T, rgdCount int, listErr error) *clusters.Cluster {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				ul, ok := list.(*unstructured.UnstructuredList)
				if !ok {
					return c.List(ctx, list, opts...)
				}
				if listErr != nil {
					return listErr
				}
				for i := 0; i < rgdCount; i++ {
					u := unstructured.Unstructured{}
					u.SetGroupVersionKind(schema.GroupVersionKind{Group: kroAPIGroup, Version: kroAPIVersion, Kind: "ResourceGraphDefinition"})
					u.SetName("rgd-" + string(rune('a'+i)))
					ul.Items = append(ul.Items, u)
				}
				return nil
			},
		}).
		Build()
	return clusters.NewTestClusterFromClient("mcp", fakeClient)
}

func TestCountResourceGraphDefinitions(t *testing.T) {
	tests := []struct {
		name     string
		rgdCount int
		listErr  error
		want     int
		wantErr  bool
	}{
		{name: "none present", rgdCount: 0, want: 0},
		{name: "some present", rgdCount: 3, want: 3},
		{
			name:    "kro CRD not installed -> treated as none",
			listErr: &apimeta.NoKindMatchError{GroupKind: schema.GroupKind{Group: kroAPIGroup, Kind: "ResourceGraphDefinition"}},
			want:    0,
		},
		{
			name:    "unexpected error propagates",
			listErr: errors.New("boom"),
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &KroReconciler{}
			got, err := r.countResourceGraphDefinitions(context.Background(), fakeMCPCluster(t, tc.rgdCount, tc.listErr))
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDelete_BlockedByResourceGraphDefinitions(t *testing.T) {
	obj := &apiv1alpha1.Kro{
		ObjectMeta: metav1.ObjectMeta{Name: "mcp-01", Namespace: "tenant"},
	}
	r := &KroReconciler{}
	clusterCtx := clusteraccess.ClusterContext{MCPCluster: fakeMCPCluster(t, 2, nil)}

	res, err := r.Delete(context.Background(), obj, &apiv1alpha1.ProviderConfig{}, clusterCtx)
	require.NoError(t, err)

	// Deletion is held: we requeue instead of tearing down and dropping the finalizer.
	assert.Equal(t, deletionBlockedRequeue, res.RequeueAfter)
	assert.Equal(t, spruntime.StatusPhaseTerminating, obj.Status.Phase)

	cond := apimeta.FindStatusCondition(obj.Status.Conditions, spruntime.ServiceProviderConditionReady)
	require.NotNil(t, cond)
	assert.Contains(t, cond.Message, "deletion blocked")
	assert.Contains(t, cond.Message, "2 kro ResourceGraphDefinition")
}
