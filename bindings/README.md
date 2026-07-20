# fleet-bindings

Helm chart that installs the [fleet binding controller](../controllers/fleet-binding-controller)
and, optionally, renders static `ArgoCDApplication` bindings. See the
[root README](../README.md) for how this fits into the fleet's two-layer
GitOps design.

## Prerequisites

- A Kubernetes cluster with the vCluster Platform CRDs installed
  (`clusters.management.loft.sh`, `virtualclusterinstances.management.loft.sh`,
  `argocdapplications.management.loft.sh`, and `accesskeys.storage.loft.sh`).
- The target namespace (`controller.namespace`, default `vcluster-platform`)
  already exists. This chart does not create it.
- The Platform project namespace (`projectNamespace`, default `p-platform`)
  already exists. Central `FleetProfile` and Cluster-targeted generated
  `ArgoCDApplication` resources live there; VCI-targeted applications live in
  each VCI's project namespace.
- If you're not using the default `controller.image.tag`, build and push the
  controller image first (see the
  [controller README](../controllers/fleet-binding-controller/README.md)).

## Primary path: GitOps

In this repo, this chart is installed by Argo CD via
[`bootstrap/fleet-bindings.yaml`](../bootstrap/fleet-bindings.yaml), applied
after `clusters/` and `baseline/` (sync wave ordering, see the root README).
No manual `helm install` is needed for a normal fleet deployment — push to
`main` and Argo CD reconciles it.

## Manual install (local testing, or standalone use)

Inspect and render the published chart before installing:

```sh
helm show chart \
  oci://ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-bindings \
  --version 0.5.0
helm template fleet-bindings \
  oci://ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-bindings \
  --version 0.5.0 \
  --namespace vcluster-platform
```

Install or upgrade:

```sh
helm upgrade --install fleet-bindings \
  oci://ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-bindings \
  --version 0.5.0 \
  --namespace vcluster-platform \
  --create-namespace
```

Override values inline, or with `-f my-values.yaml`:

```sh
helm upgrade --install fleet-bindings \
  oci://ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-bindings \
  --version 0.5.0 \
  --namespace vcluster-platform \
  --set controller.image.tag=0.2.0 \
  --set controller.reconcileInterval=15s
```

Uninstall:

```sh
helm uninstall fleet-bindings --namespace vcluster-platform
```

Uninstalling removes the controller Deployment/RBAC/ConfigMap and rendered
`FleetProfile` resources. Helm intentionally leaves the CRD itself installed.
It does **not** remove the `ArgoCDApplication` bindings the controller generated;
clean them up explicitly if needed:

```sh
kubectl delete argocdapplications.management.loft.sh -A \
  -l fleet.lab.kurtmadel.com/generated-by=fleet-binding-controller
kubectl delete accesskeys.storage.loft.sh \
  -l fleet.lab.kurtmadel.com/generated-by=fleet-binding-controller
```

Delete the generated tenant Secrets before uninstalling if VCIs will remain;
after the controller is gone it cannot reach each tenant to remove them.

## Verifying

```sh
kubectl -n vcluster-platform get pods -l app.kubernetes.io/name=fleet-binding-controller
kubectl -n vcluster-platform logs deploy/fleet-binding-controller -f
kubectl -n p-platform get fleetprofiles.fleet.lab.kurtmadel.com
kubectl -n p-platform get argocdapplications.management.loft.sh \
  -l fleet.lab.kurtmadel.com/generated-by=fleet-binding-controller
```

## Key values

| Value | Purpose |
|-------|---------|
| `projectNamespace` | Central FleetProfile namespace and namespace for Cluster-targeted bindings |
| `profiles` | `FleetProfile` definitions, including application dependencies |
| `controller.enabled` | Deploy the controller Deployment/RBAC/ConfigMap |
| `controller.image.repository` / `.tag` / `.digest` | Controller image; an empty tag uses `Chart.yaml` `appVersion`, while `.digest` optionally pins beyond the tag |
| `controller.clusterSelector` | Label selector for which `Cluster` resources are eligible |
| `controller.virtualClusters.*` | Automatic VCI profile selection and its opt-out/override annotations |
| `controller.writerCredentials.*` | Per-VCI writer key and tenant Secret delivery settings |
| `controller.reconcileInterval` | Poll interval (e.g. `30s`, `5m`) |
| `staticBindings.enabled` | Alternative mode: render bindings from `staticBindings.clusters` instead of running the controller |

See [`values.yaml`](values.yaml) for the full set and defaults.

## Automatic tenant observability

The chart renders two central FleetProfiles:

| Rendered VCI shape | FleetProfile | Template |
| --- | --- | --- |
| `privateNodes.enabled: true` | `tenant-observability-private-nodes` | Platform `cluster-collector` |
| Shared Nodes | `tenant-observability-shared-nodes` | `shared-node-tenant-collector` from `baseline/` |

VCIs are discovered cluster-wide, and each generated `ArgoCDApplication` is
created in that VCI's project namespace. FleetProfiles remain central in
`projectNamespace`. A VCI must have a usable Argo CD connector so Platform can
resolve the generated `virtualCluster` destination.

Before enabling writer credentials, set `platformURL` to an address reachable
from the controller pod. Use `platformCASecretName` for a private CA;
`insecureSkipVerify` is for temporary demonstrations only. The collector
templates and controller must agree on the `otel-otlp-auth` Secret name.

The controller refuses to overwrite or delete an existing Secret of that name
unless it carries
`fleet.lab.kurtmadel.com/generated-by=fleet-binding-controller`. Before
migrating a manually managed tenant, delete its old manual
`ArgoCDApplication` and `otel-otlp-auth` Secret (or explicitly label the Secret
for adoption). This also avoids running the old and automatic collectors at the
same time.

```sh
kubectl get virtualclusterinstances.management.loft.sh -A
kubectl get argocdapplications.management.loft.sh -A \
  -l fleet.lab.kurtmadel.com/source-kind=virtualclusterinstance
kubectl get accesskeys.storage.loft.sh \
  -l fleet.lab.kurtmadel.com/credential-purpose=metrics-writer
```

## Per-binding template parameters

When an upstream `ArgoCDApplicationTemplate` requires a parameter that it does
not resolve directly from `.Values.loft.clusterAnnotations`, put the concrete
string value on the destination `Cluster` using:

```text
<templateRef.name>.argocd-template-param.fleet.lab.kurtmadel.com/<exact-parameter-name>
```

Example:

```yaml
metadata:
  annotations:
    example-dashboard.argocd-template-param.fleet.lab.kurtmadel.com/externalHost: dashboard.example.com
```

This generates `spec.parameters.externalHost` only on the binding whose
`templateRef.name` is `example-dashboard`. Removing the annotation
removes the generated parameter on the next reconciliation. Parameter names
are exact and case-sensitive, and annotations must not contain secrets.

## Notes on this chart's design

- Resource names are fixed (not `{{ .Release.Name }}`-prefixed) because this
  chart is a cluster singleton — one install per vCluster Platform management
  cluster. Installing it twice in the same cluster will collide on the
  cluster-scoped `ClusterRole`/`ClusterRoleBinding`.
- `controller.replicaCount` should stay `1`: the controller has no leader
  election, so extra replicas just add redundant API calls, not concurrency.
- Dependencies gate initial rollout only. A deployed dependent is not removed
  merely because a prerequisite becomes temporarily unhealthy.
- Missing dependencies and cycles preserve the affected Cluster's existing
  bindings. Fix the profile graph before expecting further reconciliation.
