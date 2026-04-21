package e2e

import (
	"context"
	"testing"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"
	"github.com/openmcp-project/openmcp-testing/pkg/clusterutils"
	"github.com/openmcp-project/openmcp-testing/pkg/conditions"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"github.com/openmcp-project/openmcp-testing/pkg/providers"
	"github.com/openmcp-project/openmcp-testing/pkg/resources"
)

func TestServiceProvider(t *testing.T) {
	var onboardingList unstructured.UnstructuredList
	mcpName := "test-mcp"
	basicProviderTest := features.New("provider test").
		Setup(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			if _, err := resources.CreateObjectsFromDir(ctx, c, "platform"); err != nil {
				t.Errorf("failed to create platform cluster objects: %v", err)
			}
			return ctx
		}).
		Setup(providers.CreateMCP(mcpName)).
		Assess("verify service can be successfully consumed",
			func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
				onboardingConfig, err := clusterutils.OnboardingConfig()
				if err != nil {
					t.Error(err)
					return ctx
				}
				objList, err := resources.CreateObjectsFromDir(ctx, onboardingConfig, "onboarding")
				if err != nil {
					t.Errorf("failed to create onboarding cluster objects: %v", err)
					return ctx
				}
				for _, obj := range objList.Items {
					if err := wait.For(conditions.Match(&obj, onboardingConfig, "Ready", corev1.ConditionTrue)); err != nil {
						t.Error(err)
					}
				}
				objList.DeepCopyInto(&onboardingList)
				return ctx
			},
		). // Add Assess to verify that helmRelese and Oci Repo are Ready.
		Assess("verify that helm release and oci repository are ready", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			tenantNamespace, err := libutils.StableMCPNamespace(mcpName, "default")
			if err != nil {
				t.Errorf("failed to get tenant namespace: %v", err)
				return ctx
			}

			helmRelease := &helmv2.HelmRelease{}
			helmRelease.SetName("kro-helm-release")
			helmRelease.SetNamespace(tenantNamespace)

			ociRepo := &sourcev1.OCIRepository{}
			ociRepo.SetName("kro-oci-repository")
			ociRepo.SetNamespace(tenantNamespace)

			if err := wait.For(conditions.Match(helmRelease, config, "Ready", corev1.ConditionTrue), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("HelmRelease not ready: %v", err)
			}
			if err := wait.For(conditions.Match(ociRepo, config, "Ready", corev1.ConditionTrue), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("OCIRepository not ready: %v", err)
			}

			return ctx
		}).
		Assess("verify domain objects can be created", providers.ImportDomainAPIs(mcpName, "mcp")).
		Teardown(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			onboardingConfig, err := clusterutils.OnboardingConfig()
			if err != nil {
				t.Error(err)
				return ctx
			}
			for _, obj := range onboardingList.Items {
				if err := resources.DeleteObject(ctx, onboardingConfig, &obj, wait.WithTimeout(time.Minute)); err != nil {
					t.Errorf("failed to delete onboarding object: %v", err)
				}
			}
			return ctx
		}).
		Teardown(providers.DeleteMCP("test-mcp", wait.WithTimeout(5*time.Minute)))
	testenv.Test(t, basicProviderTest.Feature())
}
