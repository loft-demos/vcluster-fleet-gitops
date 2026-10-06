# Release Notes

## 0.6.1

### Fixed: the writer Secret sync kept waking sleeping tenant clusters

`fleet-binding-controller` syncs the `observability/otel-otlp-auth` Secret into every enrolled tenant cluster every `writerCredentials.syncInterval` (10 minutes by default) and on every controller restart. It does this through the vCluster Platform proxy with a temporary installer AccessKey owned by `writerCredentials.installerUser`. That key was an ordinary user key, so vCluster Platform treated each sync as tenant activity:

- A sleeping tenant cluster was woken by the sync's `GET` on the Secret.
- An awake tenant cluster had its inactivity timer reset.

With a short `sleep.auto.afterInactivity` (for example `300s`), enrolled tenant clusters were woken roughly every sync interval and spent about half their time awake. The VCI's `sleepmode.loft.sh/last-activity-info` annotation showed the cause as `loft:user:<installerUser>` doing `get secrets/otel-otlp-auth`.

This release changes two things:

- **Sleeping tenant clusters are skipped.** The controller now defers the Secret sync while a `VirtualClusterInstance` is asleep. It treats a tenant cluster as asleep when any of these is true:
  - it has the `sleepmode.loft.sh/sleeping-since` annotation,
  - `status.sleepModeConfig.status.sleepingSince` is set,
  - `status.phase` is `Sleeping`.

  The tenant cluster keeps the Secret while it sleeps. The sync runs on the first reconcile pass after it wakes. The tenant cluster's Applications are still reconciled as before.
- **The installer key no longer counts as activity.** The temporary installer AccessKey now carries `sleepmode.loft.sh/ignore-activity: "true"`, so its requests neither reset a tenant cluster's inactivity timer nor wake it. If a sync races with the tenant cluster falling asleep, the request fails with a `502` and is retried on a later pass.

### Upgrade notes

- No configuration changes are needed.
- The label requires vCluster Platform v4.12.0 or later. Earlier versions ignore it, but sleeping tenant clusters are still skipped.
- When a sleeping tenant cluster opts out of observability, removing its Secret now fails while it is asleep and is retried on later passes, without waking it. The controller keeps its existing Applications until the removal succeeds.
