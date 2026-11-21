---
title: What the driver reports
weight: 30
---

# What the driver reports

The node plugin and the controller each serve Prometheus metrics at
`/metrics` on the container port named `metrics`, which is `9200` by
default. The base in `deploy/` works without Prometheus. If your
cluster runs the prometheus-operator, add the `deploy/monitoring`
component next to the base, and Prometheus scrapes both pods:

```yaml
resources:
  - https://github.com/liken-sh/liken//git-csi-driver/deploy?ref=<tag>
components:
  - https://github.com/liken-sh/liken//git-csi-driver/deploy/monitoring?ref=<tag>
```

| Component | Metric | Type | What it shows |
| --- | --- | --- | --- |
| git-csi-driver | `liken_build_info{component, version}` | gauge | the release this process runs, always `1`. Every `liken` component exports this metric, so one graph shows the versions of all of them |
| git-csi-driver | `git_csi_volumes{repo}` | gauge | the number of volumes mounted from each repository. `repo` is the driver's short name for the repository, without the URL or a credential |
| git-csi-driver | `git_csi_fetch_duration_seconds{repo}` | histogram | how long each fetch from the forge takes |
| git-csi-driver | `git_csi_fetch_failures_total{repo}` | counter | fetches from the forge that failed |
| git-csi-driver | `git_csi_store_bytes` | gauge | the bytes that the bare repositories and work trees in the node's store take on disk |
| git-csi-driver | `git_csi_armed{namespace, claim}` | gauge | one while the claim uses a class of this driver, so the driver commits and pushes, zero otherwise |
| git-csi-driver | `git_csi_pending_paths{namespace, claim}` | gauge | paths the last scan found that the driver has not committed |
| git-csi-driver | `git_csi_unpushed_commits{namespace, claim}` | gauge | commits in the tree that are not yet on the remote |
| git-csi-driver | `git_csi_last_push_timestamp_seconds{namespace, claim}` | gauge | when the last push to the remote succeeded, in seconds since the epoch |
| git-csi-driver | `git_csi_push_failures_total{namespace, claim}` | counter | pushes to the remote that failed |
| git-csi-driver | `git_csi_skipped_files{namespace, claim}` | gauge | files that the commits leave out: the files that the last commit left out because they are larger than `commit.maxFileSize`, and the files that the pod wrote outside the volume's `paths` and that are still in the tree |
| git-csi-driver | `git_csi_diverged{namespace, claim}` | gauge | one while the volume pushes to its side branch, zero while it pushes to its ref |
| git-csi-driver | `git_csi_volume_abnormal{namespace, volume}` | gauge | one while any problem listed under [the abnormal gauge](../classes/#the-abnormal-gauge) is true for the volume, zero otherwise. `volume` is the `volumeHandle`, so this metric also covers read-only volumes, which have no claim of their own |
| git-csi-driver | `git_csi_demanded_pulls_total{namespace, volume}` | counter | pulls started by a demand on the volume's `PersistentVolume` |
| git-csi-driver | `git_csi_reconcile_duration_seconds{kind}` | histogram | how long each CSI call takes to return, by call. The kubelet waits for the call, so a slow call delays the pod |
| git-csi-driver | `git_csi_reconcile_errors_total{kind}` | counter | CSI calls that failed, which the kubelet retries |
| git-csi-driver | `git_csi_watch_restarts_total{kind}` | counter | times the node plugin opened a watch again after it ended, by the kind it watches. The node plugin runs one watch of `PersistentVolume`s, and one watch of a `PersistentVolumeClaim` for each writeable volume. client-go ends each watch after 5 to 10 minutes and opens it again, so a healthy watch restarts 6 to 12 times an hour. A higher rate means watches that fail soon after they open. A refused watch is not counted, so a rate near zero can mean the API server refuses every watch |
| git-csi-driver | `git_csi_webhook_requests_total{result}` | counter | webhook requests the controller handled, by `result`: `accepted`, `unauthenticated`, `malformed`, or `failed` |
| git-csi-driver | `git_csi_webhook_marked_total` | counter | `PersistentVolume`s that the controller marked for a pull after a push that it verified |

The [writeable guide](../../guides/writeable/) and the
[read-only guide](../../guides/read-only/) show how to read several of
these metrics.
