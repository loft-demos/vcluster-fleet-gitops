# fleet-bindings

Helm chart that installs the [fleet binding controller](../controllers/fleet-binding-controller)
and, optionally, renders static `ArgoCDApplication` bindings. See the
[root README](../README.md) for how this fits into the fleet's two-layer
GitOps design.

## Prerequisites

- A Kubernetes cluster with the vCluster Platform CRDs installed
  (`clusters.management.loft.sh`, `argocdapplications.management.loft.sh`).
- The target namespace (`controller.namespace`, default `vcluster-platform`)
  already exists. This chart does not create it.
- The Platform project namespace (`projectNamespace`, default `p-platform`)
  already exists. `FleetProfile` and generated `ArgoCDApplication` resources
  live there.
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
  --version 0.1.0
helm template fleet-bindings \
  oci://ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-bindings \
  --version 0.1.0 \
  --namespace vcluster-platform
```

Install or upgrade:

```sh
helm upgrade --install fleet-bindings \
  oci://ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-bindings \
  --version 0.3.0 \
  --namespace vcluster-platform \
  --create-namespace
```

Override values inline, or with `-f my-values.yaml`:

```sh
helm upgrade --install fleet-bindings \
  oci://ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-bindings \
  --version 0.3.0 \
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
kubectl -n p-platform delete argocdapplications.management.loft.sh \
  -l fleet.lab.kurtmadel.com/generated-by=fleet-binding-controller
```

(replace `p-platform` with your `projectNamespace`).

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
| `projectNamespace` | Namespace the generated `ArgoCDApplication` bindings live in |
| `profiles` | `FleetProfile` definitions, including application dependencies |
| `controller.enabled` | Deploy the controller Deployment/RBAC/ConfigMap |
| `controller.image.repository` / `.tag` / `.digest` | Controller image; `.digest` optionally pins beyond the tag |
| `controller.clusterSelector` | Label selector for which `Cluster` resources are eligible |
| `controller.reconcileInterval` | Poll interval (e.g. `30s`, `5m`) |
| `staticBindings.enabled` | Alternative mode: render bindings from `staticBindings.clusters` instead of running the controller |

See [`values.yaml`](values.yaml) for the full set and defaults.

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
