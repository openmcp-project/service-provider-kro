package e2e

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"k8s.io/klog/v2"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"

	"github.com/openmcp-project/openmcp-testing/pkg/providers"
	"github.com/openmcp-project/openmcp-testing/pkg/setup"
)

var testenv env.Environment

func TestMain(m *testing.M) {
	initLogging()
	version := mustVersion()
	openmcp := setup.OpenMCPSetup{
		Namespace: "openmcp-system",
		Operator: setup.OpenMCPOperatorSetup{
			Name:         "openmcp-operator",
			Image:        "ghcr.io/openmcp-project/images/openmcp-operator:v0.18.1",
			Environment:  "debug",
			PlatformName: "platform",
		},
		ClusterProviders: []providers.ClusterProviderSetup{
			{
				Name:  "kind",
				Image: "ghcr.io/openmcp-project/images/cluster-provider-kind:v0.2.0",
			},
		},
		ServiceProviders: []providers.ServiceProviderSetup{
			{
				Name:               "kro",
				Image:              fmt.Sprintf("ghcr.io/openmcp-project/images/service-provider-kro:%s", version),
				LoadImageToCluster: true,
			},
		},
	}
	testenv = env.NewWithConfig(envconf.New().WithNamespace(openmcp.Namespace))
	openmcp.Bootstrap(testenv)
	os.Exit(testenv.Run(m))
}

func mustVersion() string {
	cmd := exec.Command("../../hack/common/get-version.sh")
	version, err := cmd.Output()
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(version))
}

func initLogging() {
	klog.InitFlags(nil)
	if err := flag.Set("v", "2"); err != nil {
		panic(err)
	}
	flag.Parse()
}
