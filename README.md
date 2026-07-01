# vcluster-fleet-gitops

GitOps fleet management for vCluster Platform (vCP), covering the Platform
management cluster, connected control-plane clusters, and tenant clusters.
Git is the source of truth; vCluster Platform is the distributor. This
repository combines two delivery mechanisms:

- The **vCP Fleet Binding Controller** watches registered `Cluster` metadata and
  dynamically binds application profiles to the Platform management cluster and
  connected control-plane clusters.
- The **v2 Argo CD integration** delivers applications referenced by
  `VirtualClusterTemplate`s to tenant clusters (`target: vCluster`) or their
  control plane clusters (`target: host`).

The repository also owns the bootstrap app-of-apps and Layer 1 Argo CD
Applications that reconcile vCP resources such as `Cluster` registrations,
`VirtualClusterTemplate`s, and `ArgoCDApplicationTemplate`s. Together, these
pieces provide a declarative path from fleet membership and templates to
workloads running across both infrastructure and tenant clusters, without
per-cluster bespoke Argo CD wiring.

## The two layers

The solution spans two layers with different ownership:

- **Layer 1 - platform configuration and fleet bindings (this repo).** Plain
  Argo CD `Application`s apply `management.loft.sh` resources to the Platform
  management cluster and install the Fleet Binding Controller. This is ordinary
  GitOps and does not use `ArgoCDApplicationTemplate` to manage its own
  bootstrap resources, because those templates must exist before vCP can use
  them.
- **Layer 2 - platform-driven delivery (lives in the workload repos).** Once
  Layer 1 has created the templates, vCP's v2 integration uses
  `deploy.argoCD.applications` on a `VirtualClusterTemplate` to reference an
  `ArgoCDApplicationTemplate` and deliver workloads to fleet members:
  `target: vCluster` for tenant clusters, `target: host` for control plane
  clusters. Example: `vcluster-snooze`.

## Bootstrap: exactly one touch, and it lives in IaC

There is always one bootstrap action. We make it the Argo CD install you already
run, so there is no separate `kubectl apply`.

```text
helm upgrade --install argocd argo/argo-cd ...  (with extraObjects seed)
        │
        ▼
  Application/root  ──recurse──►  bootstrap/
        ▲                          ├─ root.yaml                   (self-manages root)
        └──────── owns ────────────┤  snooze-platform-config.yaml (→ vcluster-snooze/vcp/manifests)
                                    └─ ...more child Applications...
```

1. The Argo CD Helm values
   ([`kpi-vcluster/manifests/argocd/argocd-values-workers.yaml`](../kpi-vcluster/manifests/argocd/argocd-values-workers.yaml))
   carry an `extraObjects:` block that **seeds** `Application/root`. So
   `helm upgrade --install argocd ...` installs Argo CD and the root app in one
   shot.
2. `root` recurses [`bootstrap/`](bootstrap/), which contains its **own**
   manifest ([`bootstrap/root.yaml`](bootstrap/root.yaml)). After the first
   reconcile, the git copy owns `root`. The seed in the Helm values is only used
   the very first time a cluster comes up; from then on every change flows
   through git.

Keep [`bootstrap/root.yaml`](bootstrap/root.yaml) and the `extraObjects` seed in
the Helm values identical.

### For the fleet

Each new control plane cluster needs that one bootstrap touch. Fold the Argo CD
`helm install` (with this values file) into the cluster provisioning IaC
(`vcluster-auto-nodes-*` terraform / ansible), and a freshly provisioned cluster
comes up already bootstrapped, zero kubectl. `argocd-autopilot` is a turnkey
alternative that installs Argo CD and commits its own manifests to git.

## Layout

```text
vcluster-fleet-gitops/
  bootstrap/
    root.yaml                     # self-managing app-of-apps (recurses bootstrap/)
    snooze-platform-config.yaml   # Layer 1: vcluster-snooze/vcp/manifests -> vCP
    fleet-clusters.yaml           # Layer 1: applies clusters/ (Cluster param sheets)
    fleet-baseline.yaml           # Layer 1: applies baseline/*.yaml (App Templates)
    fleet-bindings.yaml           # Layer 1: installs the binding controller
  clusters/                       # annotated Cluster resources = per-cluster params
    cp-blacksburg-dc1.yaml
    vcp.yaml
  baseline/                       # one ArgoCDApplicationTemplate per shared app
    cert-manager.yaml             #   uniform
    metrics-server.yaml           #   uniform
    metallb.yaml                  #   uniform (controller)
    metallb-config.yaml           #   per-cluster (pool from Cluster annotation)
    envoy-gateway.yaml            #   uniform (controller)
    envoy-gateway-config.yaml     #   per-cluster (edge: GatewayClass/EnvoyProxy/Gateway)
    cert-config.yaml              #   per-cluster (edge TLS certs via cert-manager)
    vcluster-gitops-watcher.yaml  #   management-cluster-only
    charts/metallb-config/        #   wrapper: IPAddressPool from .Values.addressPool
    charts/envoy-gateway-config/  #   wrapper: edge from base domain + LB IP + platform host
    charts/cert-config/           #   wrapper: wildcard Certificates from base domain + issuer
    manifests/vcluster-gitops-watcher/   # centralized watcher manifests
  bindings/                       # chart: controller + app profile config
    Chart.yaml
    values.yaml                   #   profiles, selector, controller image
    templates/
  controllers/
    fleet-binding-controller/     # source + Dockerfile for dynamic bindings
```

## vCP fleet management

The fleet has two complementary application-delivery paths:

- The Fleet Binding Controller creates vCP `ArgoCDApplication` bindings for the
  Platform management cluster and connected control-plane clusters.
- `VirtualClusterTemplate` resources use the v2 Argo CD integration to deliver
  applications to tenant clusters or their control plane clusters.

Both paths reuse `ArgoCDApplicationTemplate` resources. vCluster Platform is the
distributor, and Git remains the source of truth for templates, profiles, and
desired platform configuration.

The **fleet binding controller is the primary binding mechanism in this repo**.
Git defines the available application templates, profiles, selectors, and
controller configuration. Live `Cluster` metadata selects the profiles, and the
controller materializes the corresponding vCP `ArgoCDApplication` resources.

```text
Cluster labels + annotations
            |
            v
fleet-binding-controller <--- profiles in bindings/values.yaml
            |
            v
ArgoCDApplication ---> ArgoCDApplicationTemplate ---> target cluster
```

The unit of delivery is a vCP **`ArgoCDApplication`** (not a field on the
`Cluster` resource) created in the project namespace, pinned to a cluster with
`spec.destination.cluster.name`, and pointing at a reusable
**`ArgoCDApplicationTemplate`** via `spec.templateRef.name`.

The design rules:

- **One `ArgoCDApplicationTemplate` per app, not per cluster.** A second template
  for the same app only when the divergence is structural (different chart or
  config shape), not just a value.
- **Per-cluster divergence lives on the `Cluster` resource as annotations.** The
  vCP reconciler exposes the target Cluster's annotations under
  `.Values.loft.clusterAnnotations` (the same path the tenant flow uses), so a
  template reads, for example,
  `{{ index .Values.loft.clusterAnnotations "fleet.lab.kurtmadel.com/metallb-pool" }}`.
  See [`baseline/metallb-config.yaml`](baseline/metallb-config.yaml). This is
  read-only: a template cannot write annotations back onto the `Cluster`, so the
  annotations are set on the `Cluster` object in git (and/or by provisioning IaC).
- **Bindings carry no per-cluster values.** Because the values come off the
  Cluster, each `ArgoCDApplication` is just `name` + `destination.cluster.name` +
  `templateRef`. The fleet binding controller watches `Cluster` metadata and
  creates those bindings from profiles in [`bindings/values.yaml`](bindings/values.yaml).

Value precedence: template defaults, then `Cluster` annotations (per-cluster),
then `ArgoCDApplication` parameters (per-binding override).

### Primary: Fleet binding controller

The controller selects `Cluster` resources by label, reads one or more profile
names from annotations, and reconciles the generated `ArgoCDApplication`
resources in the platform project namespace. Adding or changing matching
`Cluster` metadata takes effect on the next reconcile without changing the
binding chart.

A `Cluster` is only reconciled when it matches the label selector below AND
has `spec.argoCD.enabled: true`. Clusters missing that field, or with it set
to `false`, are skipped entirely, and any bindings the controller previously
generated for them are pruned on the next reconcile.

Default selector:

```yaml
metadata:
  labels:
    fleet.lab.kurtmadel.com/baseline: "true"
```

Required spec gate:

```yaml
spec:
  argoCD:
    enabled: true
```

Profile selection:

```yaml
metadata:
  annotations:
    fleet.lab.kurtmadel.com/profiles: control-plane-baseline
```

Profiles can be combined:

```yaml
metadata:
  annotations:
    fleet.lab.kurtmadel.com/profiles: control-plane-baseline,gpu-nvidia-baseline
```

The controller also supports app-level adjustments without defining a new
profile:

| Annotation | Meaning | Example |
|------------|---------|---------|
| `fleet.lab.kurtmadel.com/profiles` | Comma-separated app profiles | `control-plane-baseline,gpu-nvidia-baseline` |
| `fleet.lab.kurtmadel.com/extra-apps` | Extra templateRefs to bind | `custom-edge-config` |
| `fleet.lab.kurtmadel.com/skip-apps` | Profile apps to omit | `metallb` |

Generated `ArgoCDApplication` objects are labelled with
`fleet.lab.kurtmadel.com/generated-by=fleet-binding-controller`. The controller
only prunes resources carrying that label, so hand-created or older static
bindings are not deleted unless the controller has adopted them by reconciling
the same name.

The controller image source lives in
[`controllers/fleet-binding-controller`](controllers/fleet-binding-controller).
Build and publish it, then update `controller.image.repository` and
`controller.image.tag` in [`bindings/values.yaml`](bindings/values.yaml).

### Alternative: Static GitOps bindings

For environments that require every `ArgoCDApplication` binding to be fully
materialized from Git, the chart retains a static mode. This mode does not react
to live `Cluster` labels or annotations; every cluster-to-app relationship must
be added explicitly.

Disable the controller and configure `staticBindings`:

```yaml
controller:
  enabled: false

staticBindings:
  enabled: true
  clusters:
    - name: cp-blacksburg-dc1
      apps:
        - cert-manager
        - metrics-server
        - metallb
        - metallb-config
        - envoy-gateway
        - envoy-gateway-config
        - cert-config
```

With static mode, set `spec.syncPolicy.automated.prune: true` in
[`bootstrap/fleet-bindings.yaml`](bootstrap/fleet-bindings.yaml) if Argo CD
should delete bindings removed from Git. The default controller mode leaves
Argo CD pruning disabled because the controller owns generated-binding cleanup.

Static mode is simpler operationally because it has no controller image, but it
duplicates cluster membership in Git and requires a commit for every binding
change. Use it as an explicit policy choice, not as the default path.

### Uniform vs per-cluster apps

Apps that are identical everywhere (cert-manager, metrics-server, the MetalLB
controller, the Envoy Gateway controller) are single uniform templates. Apps
whose config diverges per cluster get a second template paired with a tiny
wrapper chart:

- **`metallb-config`** renders the `IPAddressPool` from
  `fleet.lab.kurtmadel.com/metallb-pool`.
- **`envoy-gateway-config`** renders the `GatewayClass`, the `EnvoyProxy`
  (MetalLB edge IP from `fleet.lab.kurtmadel.com/gateway-lb-ip`), and the `lab`
  `Gateway` whose listeners derive from `fleet.lab.kurtmadel.com/base-domain`
  (`*.<base>`, `*.apps.<base>`). The Platform management-cluster-only `vcp-tls`
  passthrough listener is added only when
  `fleet.lab.kurtmadel.com/platform-hostname` is present, so the same template
  fits the management and connected control-plane clusters.
- **`cert-config`** closes the TLS loop: it issues the wildcard secrets the edge
  listeners reference (`<base-dashed>-tls`, `apps-<base-dashed>-tls`) via
  cert-manager, using `fleet.lab.kurtmadel.com/cluster-issuer`. It deploys into
  `envoy-gateway-system` so the Gateway's name-only `certificateRefs` resolve.
  Needs cert-manager CRDs + a DNS01 issuer (wildcards); Argo CD retries until
  they exist.

The `Cluster` annotations are the fleet parameter sheet:

| Annotation | Used by | Example |
|------------|---------|---------|
| `fleet.lab.kurtmadel.com/profiles` | `fleet-binding-controller` | `control-plane-baseline` |
| `fleet.lab.kurtmadel.com/extra-apps` | `fleet-binding-controller` | `custom-app` |
| `fleet.lab.kurtmadel.com/skip-apps` | `fleet-binding-controller` | `metallb` |
| `fleet.lab.kurtmadel.com/metallb-pool` | `metallb-config` | `192.168.51.0/24` |
| `fleet.lab.kurtmadel.com/gateway-lb-ip` | `envoy-gateway-config` | `192.168.51.10` |
| `fleet.lab.kurtmadel.com/base-domain` | `envoy-gateway-config`, `cert-config` | `dc1.lab.kurtmadel.com` |
| `fleet.lab.kurtmadel.com/cluster-issuer` | `cert-config` | `letsencrypt-prod` |
| `fleet.lab.kurtmadel.com/platform-hostname` | `envoy-gateway-config` (Platform management cluster only) | `vcp.lab.kurtmadel.com` |

The `gateway-lb-ip` must sit inside the cluster's `metallb-pool`, and the
`cluster-issuer` must be a DNS01 issuer that can sign wildcards.

### Profiles

Profiles are named app sets in [`bindings/values.yaml`](bindings/values.yaml).
The built-in profiles are:

| Profile | Purpose |
|---------|---------|
| `control-plane-baseline` | Shared control-plane stack: cert-manager, metrics-server, MetalLB, Envoy Gateway, and per-cluster edge/cert config |
| `vcp-management-cluster-baseline` | The control-plane baseline plus `vcluster-gitops-watcher`; use this for the Platform management cluster |
| `gpu-nvidia-baseline` | Reserved NVIDIA GPU profile for `nvidia-gpu-operator` and `nvidia-dra-driver-gpu` |
| `gpu-amd-baseline` | Reserved AMD GPU profile for `amd-gpu-operator` and `amd-dra-driver` |

GPU profiles are deliberately just profile entries until the matching
`ArgoCDApplicationTemplate`s exist in `baseline/`. Add those templates first,
then assign the profile to a cluster. That keeps a profile annotation from
creating a binding to a missing template.

### The Platform management cluster is a fleet member

The Platform management cluster is registered as a `Cluster` too
([`clusters/vcp.yaml`](clusters/vcp.yaml)) and receives the
`vcp-management-cluster-baseline` profile through its own bindings and
annotations. The one carve-out: management-cluster-critical components (the
Envoy Gateway that exposes Argo CD and the platform, and the cert-manager their
TLS depends on) are seeded in the same IaC step that installs Argo CD, then
adopted and self-healed by their bindings. Argo CD cannot install from zero the
components it needs to reach the Platform management cluster.

## Sync ordering

Children use `argocd.argoproj.io/sync-wave` so platform definitions land before
anything references them:

- `root` -> wave `-2`
- `fleet-clusters`, `fleet-baseline`, `snooze-platform-config`
  (Cluster sheets + App Templates) -> wave `-1`
- `fleet-bindings` and any `VirtualClusterInstance` that references a template
  -> default wave (later)

`fleet-bindings` intentionally leaves Argo CD automated prune disabled. The
binding controller owns pruning for generated `ArgoCDApplication` resources, and
that setting lets it adopt the old Helm-rendered bindings during migration.

## Usage

1. Create this repo in Forgejo and push it (the seed points at
   `https://forgejo.apps.lab.kurtmadel.com/vcluster-demos/platform-config.git`,
   branch `main`). Update `repoURL` everywhere if you host it elsewhere.
2. Set the `destination.namespace` on the Layer-1 apps in `bootstrap/` to the
   namespace vCluster Platform is installed in, and set `projectNamespace` in
   [`bindings/values.yaml`](bindings/values.yaml) to your platform project
   namespace (`p-<project>`).
   - Confirm the vCP cluster's real name (`kubectl get clusters.management.loft.sh`)
     and update [`clusters/vcp.yaml`](clusters/vcp.yaml) if it differs.
   - Confirm the `ArgoCDApplication` field names against your platform version
     (`kubectl explain argocdapplications.management.loft.sh --recursive`).
   - Add a `Cluster` resource per control plane cluster with its
     `fleet.lab.kurtmadel.com/*` annotations, the
     `fleet.lab.kurtmadel.com/baseline=true` label, and a
     `fleet.lab.kurtmadel.com/profiles` annotation.
   - Build and publish the fleet binding controller image from
     [`controllers/fleet-binding-controller`](controllers/fleet-binding-controller),
     then set `controller.image.repository` and `controller.image.tag`.
3. Install or upgrade Argo CD with the seeded values:

   ```sh
   helm upgrade --install argocd argo/argo-cd \
     --namespace argocd --create-namespace \
     -f ../kpi-vcluster/manifests/argocd/argocd-values-workers.yaml
   ```

4. Watch it converge:

   ```sh
   kubectl -n argocd get applications
   ```