# fleet-binding-controller

Small in-cluster reconciler that turns selected vCluster Platform `Cluster`
resources into `ArgoCDApplication` bindings.

A `Cluster` is only processed when it both matches the configured cluster
selector AND has `spec.argoCD.enabled: true`.

Build and publish:

```sh
docker build -t ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-binding-controller:0.3.0 .
docker push ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-binding-controller:0.3.0
```

Run unit tests:

```sh
go test ./...
```

The controller lists namespaced `FleetProfile` resources from the Platform
project namespace. The cluster selector remains in the JSON file mounted by the
`bindings` Helm chart.

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
