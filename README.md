# service-provider-kro

An [OpenControlPlane](https://github.com/openmcp-project) Service Provider that installs and manages
[Kro](https://kro.run) on workload clusters via Flux HelmReleases.

[![REUSE status](https://api.reuse.software/badge/github.com/openmcp-project/service-provider-kro)](https://api.reuse.software/info/github.com/openmcp-project/service-provider-kro)

## How It Works

When a `Kro` resource is created on the onboarding cluster, the controller:

1. Resolves `spec.version` from the `Kro` resource against `spec.versions` in the `ProviderConfig`, which yields the chart URL, chart version, pull secret, and Helm values for that version
2. Replicates the version's chart pull secret into the tenant namespace and wires it into the `OCIRepository`
3. Creates a Flux `OCIRepository` pointing at the resolved chart URL and chart version
4. Creates a Flux `HelmRelease` that deploys the chart into `kro-system` on the workload cluster via a kubeconfig reference

## API Reference

### Kro

The domain service API. Created on the onboarding cluster, one per tenant.

```yaml
apiVersion: kro.services.open-control-plane.io/v1alpha1
kind: Kro
metadata:
  name: mcp-01 # must match your MCP cluster so it will track the right cluster
spec:
  version: v0.9.3
```

| Field          | Type     | Required | Description                                                                                            |
| -------------- | -------- | -------- | ------------------------------------------------------------------------------------------------------ |
| `spec.version` | `string` | yes      | The version to install. Must be one of the versions offered by `spec.versions` in the `ProviderConfig` |

_Note_: Any version a tenant may request has to be defined in the `ProviderConfig`. Requesting a
version that is not on offer leaves the resource in phase `Progressing` with the `Ready` condition
set to `False`, reason `ReconcileError`, and the available versions listed in its message. Nothing
progresses until the `Kro` resource or the `ProviderConfig` is corrected.

_Note_: The name of the object _**MUST**_ match the name of your MCP cluster offering. This
ensures that only one installation can exist for a given cluster.

### ProviderConfig

Cluster-scoped operational configuration. Declares which versions of kro tenants may
install, and the deployment artifacts each of those versions maps to.

```yaml
apiVersion: kro.services.open-control-plane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: kro
spec:
  pollInterval: 5m
  versions:
    - version: v0.9.1
      chartVersion: "0.9.1"
      chartURL: oci://registry.k8s.io/kro/charts/kro
    # renovate: datasource=docker depName=registry.k8s.io/kro/charts/kro
    - version: v0.9.3
      chartVersion: "0.9.3"
      chartURL: oci://registry.k8s.io/kro/charts/kro
      chartPullSecret: my-registry-secret
      helmValues:
        # See https://github.com/kubernetes-sigs/kro/blob/main/helm/values.yaml
        # for all available configuration options including:
        # - rbac.mode: "unrestricted" (default) or "aggregation", see https://kro.run/docs/advanced/access-control
        # - deployment.resources: CPU/memory limits and requests
        # - config.resourceGraphDefinitionConcurrentReconciles: parallel RGD reconcilers
        # - config.dynamicControllerConcurrentReconciles: parallel dynamic controller reconcilers
        # - config.logLevel: "info", "debug", etc.
        imagePullSecrets:
          - name: my-registry-secret
```

#### `spec`

| Field          | Type       | Required | Default | Description                                                            |
|----------------|------------|----------|---------|------------------------------------------------------------------------|
| `versions`     | `array`    | yes      | —       | The versions of kro that can be installed. Must have at least one entry |
| `pollInterval` | `duration` | no       | `1m`    | How often the controller polls for changes                             |

A version item is defined as follows:

| Field             | Type     | Required | Default                                | Description                                                                                                                                                                           |
|-------------------|----------|----------|----------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `version`         | `string` | yes      | —                                      | The tenant version this item defines. Compared against `Kro.spec.version`                                                                                                             |
| `chartVersion`    | `string` | yes      | —                                      | The Helm chart tag to install. Need not equal `version`, the tenant version may have a `v` prefix or be a product version that differs from the chart tag                             |
| `chartURL`        | `string` | no       | `oci://registry.k8s.io/kro/charts/kro` | OCI URL of the Helm chart (`oci://` prefix is added automatically if missing)                                                                                                         |
| `chartPullSecret` | `string` | no       | —                                      | Name of a secret in the controller's namespace to replicate into the tenant namespace and set as `secretRef` on the `OCIRepository`. Must be of type `kubernetes.io/dockerconfigjson` |
| `helmValues`      | `object` | no       | —                                      | Arbitrary Helm values passed directly to the HelmRelease. Secrets under `imagePullSecrets` are replicated into the kro namespace on the control plane                                 |

Because every artifact is declared per version, two versions can point at different registries,
use different pull secrets, or have different Helm values.

Deleting a `Kro` resource does not require its version to still be on offer. It does block upgrades and re-reconciles
of instances still requesting it, so remove a version only once no instance references it.

## What is Kro

Kro (Kube Resource Orchestrator) lets you create custom Kubernetes APIs by composing existing resources into
higher-level abstractions. Check out the [Kro documentation](https://kro.run/docs/overview) for more details.

## Running E2E Tests

```shell
task test-e2e
```

## Quality Criteria

[![Quality: Incubating](https://img.shields.io/badge/Quality-Incubating-3d9970?style=flat-square&labelColor=555)](https://open-control-plane.io/developers/serviceprovider/quality-criteria)

| Criterion                         | Status | Notes                                                                                                                                                                                                                                                                                                                                                      |
|-----------------------------------|:------:|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Deletion behaviour                |   ✅   | A finalizer ensures the Service Provider managed resources like Flux' `OCIRepository` and `HelmRelease` are cleaned-up. Deletion is additionally blocked while kro `ResourceGraphDefinition` objects still exist on the `ControlPlane`, so their managed resources are not orphaned.                                                                       |
| Status reporting & error messages |   ✅   |                                                                                                                                                                                                                                                                                                                                                            |
| Operation annotations             |   ✅   | Both `openmcp.cloud/operation: ignore` (skip reconciliation) and `openmcp.cloud/operation: reconcile` (one-shot manual reconcile; the annotation is consumed) are processed.                                                                                                                                                                               |
| API stability policy              |   ✅   |                                                                                                                                                                                                                                                                                                                                                            |
| Custom CA support                 |   ✅   | Not required for kro. The platform-cluster Flux instance is assumed to be preconfigured with the necessary CA bundles for chart pulls, so no `certSecretRef` is set on the `OCIRepository`. Custom CA bundles are only needed for workloads running on the `ControlPlane` that reach private endpoints themselves (e.g. crossplane's pods pulling provider images); kro's controller has no such need. |
| Release artifacts (image + OCM)   |   ✅   |                                                                                                                                                                                                                                                                                                                                                            |
| Testing                           |   ✅   |                                                                                                                                                                                                                                                                                                                                                            |
| Ownership and maintenance docs    |   ✅   |                                                                                                                                                                                                                                                                                                                                                            |

See the [OpenControlPlane Quality Criteria](https://open-control-plane.io/developers/serviceprovider/quality-criteria) for definitions.

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
