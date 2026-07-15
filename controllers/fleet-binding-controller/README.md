# fleet-binding-controller

Small in-cluster reconciler that turns selected vCluster Platform `Cluster`
resources into `ArgoCDApplication` bindings.

A `Cluster` is only processed when it both matches the configured cluster
selector AND has `spec.argoCD.enabled: true`.

Build and publish:

```sh
docker build -t ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-binding-controller:0.4.1 .
docker push ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-binding-controller:0.4.1
```

Run unit tests:

```sh
go test ./...
```

The controller lists namespaced `FleetProfile` resources from the Platform
project namespace. The cluster selector remains in the JSON file mounted by the
`bindings` Helm chart.

An eligible `Cluster` can supply concrete parameters to a generated binding
with this annotation convention:

```text
<templateRef.name>.argocd-template-param.fleet.lab.kurtmadel.com/<exact-parameter-name>
```

For example:

```yaml
fleet-observability-grafana.argocd-template-param.fleet.lab.kurtmadel.com/platformHost: vcp.lab.kurtmadel.com
```

The controller places the resolved string under
`ArgoCDApplication.spec.parameters.platformHost`. The template and parameter
names are exact and case-sensitive. The long DNS prefix intentionally keeps the
annotation's 63-character name segment available for the parameter name. Do
not store secrets in these annotations.

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
