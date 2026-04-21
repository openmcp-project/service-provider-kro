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

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	flag "github.com/spf13/pflag"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"github.com/openmcp-project/controller-utils/pkg/clusters"
	crdutil "github.com/openmcp-project/controller-utils/pkg/crds"
	"github.com/openmcp-project/controller-utils/pkg/logging"
	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	"github.com/openmcp-project/openmcp-operator/api/common"
	openmcpconst "github.com/openmcp-project/openmcp-operator/api/constants"
	providerv1alpha1 "github.com/openmcp-project/openmcp-operator/api/provider/v1alpha1"
	"github.com/openmcp-project/openmcp-operator/lib/clusteraccess"
	"github.com/openmcp-project/openmcp-operator/lib/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/openmcp-project/service-provider-kro/api/crds"

	"github.com/openmcp-project/service-provider-kro/pkg/spruntime"

	krosv1alpha1 "github.com/openmcp-project/service-provider-kro/api/v1alpha1"
	"github.com/openmcp-project/service-provider-kro/internal/controller"
	// +kubebuilder:scaffold:imports
)

var (
	platformScheme   = runtime.NewScheme()
	onboardingScheme = runtime.NewScheme()
	mcpScheme        = runtime.NewScheme()
	setupLog         = ctrl.Log.WithName("setup")
)

func init() {
	// +kubebuilder:scaffold:scheme
	initPlatformScheme()
	initOnboardingScheme()
	initMcpScheme()
}

func initPlatformScheme() {
	utilruntime.Must(clientgoscheme.AddToScheme(platformScheme))
	utilruntime.Must(apiextensionv1.AddToScheme(platformScheme))
	utilruntime.Must(krosv1alpha1.AddToScheme(platformScheme))
	utilruntime.Must(clustersv1alpha1.AddToScheme(platformScheme))
	utilruntime.Must(providerv1alpha1.AddToScheme(platformScheme))

	utilruntime.Must(sourcev1.AddToScheme(platformScheme))
	utilruntime.Must(helmv2.AddToScheme(platformScheme))
}

func initOnboardingScheme() {
	utilruntime.Must(clientgoscheme.AddToScheme(onboardingScheme))
	utilruntime.Must(apiextensionv1.AddToScheme(onboardingScheme))
	utilruntime.Must(krosv1alpha1.AddToScheme(onboardingScheme))
}

func initMcpScheme() {
	utilruntime.Must(clientgoscheme.AddToScheme(mcpScheme))
	utilruntime.Must(apiextensionv1.AddToScheme(mcpScheme))
	utilruntime.Must(sourcev1.AddToScheme(mcpScheme))
	utilruntime.Must(helmv2.AddToScheme(mcpScheme))
}

// nolint:gocyclo
func main() {
	var command string
	var environment, providerName string
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&environment, "environment", "", "Name of the environment")
	flag.StringVar(&providerName, "provider-name", "", "Name of the provider resource")
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")

	logging.InitFlags(flag.CommandLine) // add standard logging flags

	// extract command from os.Args if present to allow further flag parsing
	if len(os.Args) > 1 {
		command = os.Args[1] // either init or run
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
	}

	flag.Parse()

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	// start sp specifics
	log, err := logging.GetLogger()
	if err != nil {
		setupLog.Error(err, "Failed to get logger")
		os.Exit(1)
	}
	ctrl.SetLogger(log.Logr())
	platformCluster, err := initializePlatformCluster()
	if err != nil {
		setupLog.Error(err, "Failed to initialize platform cluster")
		os.Exit(1)
	}
	podNamespace := os.Getenv(openmcpconst.EnvVariablePodNamespace)
	if podNamespace == "" {
		setupLog.Error(fmt.Errorf("environment variable %s not set - cannot determine source namespace for secrets", openmcpconst.EnvVariablePodNamespace), "pod namespace missing")
		os.Exit(1)
	}
	// TODO: define minimum set of permission required to run the init and run part of your service provider
	adminPermissions := []clustersv1alpha1.PermissionsRequest{
		{

			Rules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{"*"},
					Resources: []string{"*"},
					Verbs:     []string{"*"},
				},
			},
		},
	}
	clusterAccessManager := clusteraccess.NewClusterAccessManager(platformCluster.Client(),
		"kro.kro.services.openmcp.cloud", os.Getenv("POD_NAMESPACE"))
	clusterAccessManager.WithLogger(&log).
		WithInterval(10 * time.Second).
		WithTimeout(30 * time.Minute)
	ctx := context.Background()
	// init (job that installs CRDs)
	if command == "init" {
		onboardingCluster, err := clusterAccessManager.CreateAndWaitForCluster(ctx, "onboarding-init",
			clustersv1alpha1.PURPOSE_ONBOARDING, onboardingScheme, adminPermissions)

		if err != nil {
			setupLog.Error(err, "Failed to create and wait for onboarding cluster access")
		}

		crdManager := crdutil.NewCRDManager(openmcpconst.ClusterLabel, crds.CRDs)

		crdManager.AddCRDLabelToClusterMapping(clustersv1alpha1.PURPOSE_PLATFORM, platformCluster)
		crdManager.AddCRDLabelToClusterMapping(clustersv1alpha1.PURPOSE_ONBOARDING, onboardingCluster)

		if err := crdManager.CreateOrUpdateCRDs(ctx, &log); err != nil {
			setupLog.Error(err, "Failed to create or update CRDs")
		}

		spGVK := metav1.GroupVersionKind{
			Group:   krosv1alpha1.GroupVersion.Group,
			Version: krosv1alpha1.GroupVersion.Version,
			Kind:    "Kro",
		}
		if err := utils.RegisterGVKsAtServiceProvider(ctx, platformCluster.Client(), providerName, spGVK); err != nil {
			setupLog.Error(err, "Failed to register GVK at ServiceProvider")
			return
		}

		return
	}
	// run (sp controller deployment)
	onboardingCluster, err := clusterAccessManager.CreateAndWaitForCluster(ctx, "onboarding-run",
		clustersv1alpha1.PURPOSE_ONBOARDING, onboardingScheme, adminPermissions)
	if err != nil {
		setupLog.Error(err, "Failed to create and wait for onboarding cluster access")
	}
	// end sp specifics

	mgr, err := ctrl.NewManager(onboardingCluster.RESTConfig(), ctrl.Options{
		Scheme:                 onboardingScheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "232f9e39.openmcp.cloud",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}
	if err = mgr.Add(platformCluster.Cluster()); err != nil {
		setupLog.Error(err, "unable to add platform cluster to manager")
		os.Exit(1)
	}
	providerConfigUpdates := make(chan event.GenericEvent)
	spr := spruntime.NewSPReconciler[*krosv1alpha1.Kro, *krosv1alpha1.ProviderConfig](
		func() *krosv1alpha1.Kro { return &krosv1alpha1.Kro{} },
	).
		WithPlatformCluster(platformCluster).
		WithOnboardingCluster(onboardingCluster).
		WithServiceProviderReconciler(&controller.KroReconciler{
			OnboardingCluster: onboardingCluster,
			PlatformCluster:   platformCluster,
			PodNamespace:      podNamespace,
		}).
		WithClusterAccessReconciler(clusteraccess.NewClusterAccessReconciler(platformCluster.Client(), "kro.services.openmcp.cloud").
			WithMCPScheme(mcpScheme).
			WithRetryInterval(10 * time.Second).
			WithMCPPermissions(adminPermissions).WithMCPRoleRefs([]common.RoleRef{
			{
				Name: "cluster-admin",
				Kind: "ClusterRole",
			}}).
			SkipWorkloadCluster(),
		)
	if err := spr.SetupWithManager(mgr, "kro", providerConfigUpdates); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Kro")
		os.Exit(1)
	}
	pcr := spruntime.NewPCReconciler(providerName, func() *krosv1alpha1.ProviderConfig {
		return &krosv1alpha1.ProviderConfig{}
	}).
		WithPlatformCluster(platformCluster).
		WithUpdateChannel(providerConfigUpdates)
	if err := pcr.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ProviderConfig")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// initializePlatformCluster initializes the platform cluster with the necessary REST config and client.
func initializePlatformCluster() (*clusters.Cluster, error) {
	platformCluster := clusters.New("platform")

	platformCluster = platformCluster.WithRESTConfig(ctrl.GetConfigOrDie())

	if err := platformCluster.InitializeClient(platformScheme); err != nil {
		setupLog.Error(err, "Failed to initialize client for platform cluster")
		return nil, err
	}
	return platformCluster, nil
}
