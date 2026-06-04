# service-provider-kro

An [openMCP](https://github.com/openmcp-project) Service Provider that installs and manages
[Kro](https://kro.run) on workload clusters via Flux HelmReleases.

[![REUSE status](https://api.reuse.software/badge/github.com/openmcp-project/service-provider-kro)](https://api.reuse.software/info/github.com/openmcp-project/service-provider-kro)

## Quality Criteria

[![Quality: Incubating](https://img.shields.io/badge/Quality-Incubating-3d9970?style=flat-square&labelColor=555)](https://open-control-plane.io/developers/serviceprovider/quality-criteria)

| Criterion                         | Status  | Notes                                                                                                       |
| --------------------------------- | :----:  | ----------------------------------------------------------------------------------------------------------- |
| Deletion behaviour                |   ⚠️    | Finalizer is wired up; blocking deletion when kro-managed CRs still exist is not verified.                  |
| Status reporting & error messages |   ✅    |                                                                                                             |
| Operation annotations             |   ❌    | `openmcp.cloud/operation` (pause / force-reconcile) annotations are not honoured.                           |
| API stability policy              |   ✅    |                                                                                                             |
| Custom CA support                 |   ❌    | Custom CA bundle propagation to kro components is not implemented.                                          |
| Release artifacts (image + OCM)   |   ✅    |                                                                                                             |
| Testing                           |   ✅    |                                                                                                             |
| Ownership and maintenance docs    |   ✅    |                                                                                                             |

See the [OpenControlPlane Quality Criteria](https://open-control-plane.io/developers/serviceprovider/quality-criteria) for definitions.

## How It Works

When a `Kro` resource is created on the onboarding cluster, the controller:

1. Replicates the configured image pull secret into the tenant namespace and wires it into the `OCIRepository`
2. Creates a Flux `OCIRepository` pointing at the chart URL from the `ProviderConfig` and the version from the `Kro` spec
3. Creates a Flux `HelmRelease` that deploys the chart into `kro-system` on the workload cluster via a kubeconfig reference

## API Reference

### Kro

The domain service API. Created on the onboarding cluster, one per tenant.

```yaml
apiVersion: kro.services.openmcp.cloud/v1alpha1
kind: Kro
metadata:
  name: mcp-01 # must match your MCP cluster so it will track the right cluster
spec:
  # renovate: datasource=docker depName=registry.k8s.io/kro/charts/kro
  version: 0.9.2
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.version` | `string` | yes | Chart version tag |

_Note_: The name of the object _**MUST**_ match the name of your MCP cluster offering. This
ensures that only one installation can exist for a given cluster.

### ProviderConfig

Cluster-scoped operational configuration. Controls the chart location, image pull
secret replication, and Helm values passed to managed HelmReleases.

```yaml
apiVersion: kro.services.openmcp.cloud/v1alpha1
kind: ProviderConfig
metadata:
  name: kro
spec:
  pollInterval: 5m
  chartURL: oci://registry.k8s.io/kro/charts/kro
  imagePullSecret:
    name: my-registry-secret
  values:
    # See https://github.com/kubernetes-sigs/kro/blob/main/helm/values.yaml
    # for all available configuration options including:
    # - rbac.mode: "unrestricted" (default) or "aggregation", see https://kro.run/docs/advanced/access-control
    # - deployment.resources: CPU/memory limits and requests
    # - config.resourceGraphDefinitionConcurrentReconciles: parallel RGD reconcilers
    # - config.dynamicControllerConcurrentReconciles: parallel dynamic controller reconcilers
    # - config.logLevel: "info", "debug", etc.
```

#### `spec`

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `chartURL` | `string` | no | `oci://registry.k8s.io/kro/charts/kro` | OCI URL of the Helm chart (`oci://` prefix is added automatically if missing) |
| `pollInterval` | `duration` | no | `1m` | How often the controller polls for changes |
| `imagePullSecret` | `LocalObjectReference` | no | — | Secret to replicate from the controller's namespace into tenant namespaces and set as `secretRef` on the `OCIRepository` |
| `values` | `object` | no | — | Arbitrary Helm values passed directly to the HelmRelease |

## What is Kro

Kro (Kube Resource Orchestrator) lets you create custom Kubernetes APIs by composing existing resources into
higher-level abstractions. Check out the [Kro documentation](https://kro.run/docs/overview) for more details.

## Running E2E Tests

```shell
task test-e2e
```

## Support, Feedback, Contributing

This project is open to feature requests/suggestions, bug reports etc. via [GitHub issues](https://github.com/openmcp-project/service-provider-kro/issues). Contribution and feedback are encouraged and always welcome. For more information about how to contribute, the project structure, as well as additional contribution information, see our [Contribution Guidelines](CONTRIBUTING.md).

## Security / Disclosure

If you find any bug that may be a security problem, please follow our instructions in [our security policy](https://github.com/openmcp-project/service-provider-kro/security/policy) on how to report it. Please do not create GitHub issues for security-related doubts or problems.

## Code of Conduct

We as members, contributors, and leaders pledge to make participation in our community a harassment-free experience for everyone. By participating in this project, you agree to abide by its [Code of Conduct](https://github.com/openmcp-project/.github/blob/main/CODE_OF_CONDUCT.md) at all times.

## Licensing

Copyright OpenControlPlane contributors. Please see our [LICENSE](LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available [via the REUSE tool](https://api.reuse.software/info/github.com/openmcp-project/service-provider-kro).

---

<p align="center">
  <a href="https://apeirora.eu/content/projects/">
    <img alt="BMWK-EU funding logo" src="https://apeirora.eu/assets/img/BMWK-EU.png" width="300"/>
  </a>
</p>

<p align="center">
  OpenControlPlane is part of <a href="https://apeirora.eu/content/projects/">ApeiroRA</a>, an EU Important Project of Common European Interest (IPCEI-CIS).
</p>

<p align="center">
  Copyright Linux Foundation Europe. For web site terms of use, trademark policy and other project policies please see <a href="https://linuxfoundation.eu/en/policies">https://linuxfoundation.eu/en/policies</a>.
</p>
