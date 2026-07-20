# fleet-binding-controller

Small in-cluster reconciler that turns selected vCluster Platform `Cluster`
resources and rendered `VirtualClusterInstance` resources into
`ArgoCDApplication` bindings.

A `Cluster` is only processed when it both matches the configured cluster
selector AND has `spec.argoCD.enabled: true`.

When VCI observability is enabled, the controller lists VCIs across every
Platform project. It waits for `status.virtualCluster.helmRelease.values`, then
selects `tenant-observability-private-nodes` when
`privateNodes.enabled: true`; all other rendered VCIs select
`tenant-observability-shared-nodes`. The generated application lives in the
VCI's project namespace and targets `destination.virtualCluster.target:
vCluster`. No `VirtualClusterTemplate` change is required.

Set `fleet.lab.kurtmadel.com/observability-disabled: "true"` on a VCI to opt
out, or `fleet.lab.kurtmadel.com/observability-profile` to replace the automatic
observability profile. The normal `profiles` annotation remains additive.

## Per-VCI writer credentials

With credential reconciliation enabled, the controller creates one
`storage.loft.sh/v1 AccessKey` per enrolled VCI. Each key has only the
`metrics-writer` role and one exact `scope.virtualClusters` entry. The token is
written directly to `observability/otel-otlp-auth` through the Platform VCI
proxy and never placed in Git or an `ArgoCDApplication` parameter.

For each Secret operation, the controller creates a five-minute installer key
scoped to the same VCI, uses it to call the tenant Kubernetes API, and deletes
it immediately. `FLEET_BINDING_INSTALLER_USER` must be a Platform user that can
use every enrolled VCI; the chart defaults to `admin`. This is privileged, so
the chart grants the controller create/read/list/delete access to Platform
`AccessKey` resources.

The Secret is reconciled before the collector binding. Opting out deletes the
Secret and writer key before pruning the binding. When a VCI itself is deleted,
the controller removes the writer key and VCI teardown removes the Secret.

Build and publish:

```sh
docker build -t ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-binding-controller:0.6.0 .
docker push ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-binding-controller:0.6.0
```

Run unit tests:

```sh
go test ./...
```

The controller lists central namespaced `FleetProfile` resources from the
configured policy namespace. Cluster bindings use that namespace; VCI bindings
use the VCI's own project namespace. The cluster selector remains in the JSON
file mounted by the `bindings` Helm chart.

An eligible `Cluster` can supply concrete parameters to a generated binding
with this annotation convention:

```text
<templateRef.name>.argocd-template-param.fleet.lab.kurtmadel.com/<exact-parameter-name>
```

For example:

```yaml
example-dashboard.argocd-template-param.fleet.lab.kurtmadel.com/externalHost: dashboard.example.com
```

The controller places the resolved string under
`ArgoCDApplication.spec.parameters.externalHost`. The template and parameter
names are exact and case-sensitive. The long DNS prefix intentionally keeps the
annotation's 63-character name segment available for the parameter name. Do
not store secrets in these annotations.

For VCI observability, `FLEET_BINDING_VCI_OTLP_ENDPOINT` supplies a centralized
`otlpEndpoint` default to the built-in `cluster-collector` and
`shared-node-tenant-collector` bindings. A matching annotation on an individual
VCI overrides the controller default.

For dependency-aware profiles, a new binding is created only after all its
prerequisites have:

```text
status.application.health.status == "Healthy"
status.application.sync.status == "Synced"
```

Missing profiles, missing dependencies, and cycles preserve existing bindings
for the affected Cluster. Dependency health gates initial rollout only; an
already-created dependent is not removed during a transient prerequisite
failure.

The controller only prunes `ArgoCDApplication` resources labelled with
`fleet.lab.kurtmadel.com/generated-by=fleet-binding-controller`.
