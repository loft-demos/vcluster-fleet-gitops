# fleet-binding-controller

Small in-cluster reconciler that turns selected vCluster Platform `Cluster`
resources into `ArgoCDApplication` bindings.

A `Cluster` is only processed when it both matches the configured cluster
selector AND has `spec.argoCD.enabled: true`.

Build and publish:

```sh
docker build -t ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-binding-controller:0.1.0 .
docker push ghcr.io/loft-demos/vcluster-fleet-gitops/fleet-binding-controller:0.1.0
```

Run unit tests:

```sh
go test ./...
```

The controller reads profiles and cluster selectors from JSON files mounted by
the `bindings` Helm chart. It only prunes `ArgoCDApplication` resources labelled
with `fleet.lab.kurtmadel.com/generated-by=fleet-binding-controller`.
