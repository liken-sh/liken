---
title: What the driver reports
weight: 30
---

The driver serves its metrics at `/metrics` on the port named
`metrics`, `9200` by default, on both the node plugin and the
controller. The base in `deploy/` needs no Prometheus and applies
without one. A cluster owner who runs the prometheus-operator adds the
`deploy/monitoring` component beside the base to scrape both pods:

```yaml
resources:
  - https://github.com/liken-sh/git-csi-driver//deploy?ref=<tag>
components:
  - https://github.com/liken-sh/git-csi-driver//deploy/monitoring?ref=<tag>
```

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| git-csi-driver | `git_csi_volumes{repo}` | gauge | what is mounted |
| git-csi-driver | `git_csi_fetch_duration_seconds{repo}` | histogram | the forge is slow |
| git-csi-driver | `git_csi_fetch_failures_total{repo}` | counter | the forge is down |
| git-csi-driver | `git_csi_store_bytes` | gauge | growth on the wrong filesystem |
| git-csi-driver | `git_csi_armed{namespace, claim}` | gauge | one when a class of the driver arms the volume, zero when none does |
| git-csi-driver | `git_csi_pending_paths{namespace, claim}` | gauge | paths the last scan found that the driver has not committed |
| git-csi-driver | `git_csi_unpushed_commits{namespace, claim}` | gauge | commits the work tree holds that the remote does not |
| git-csi-driver | `git_csi_last_push_timestamp_seconds{namespace, claim}` | gauge | when a push to the remote last worked, in seconds since the epoch |
| git-csi-driver | `git_csi_push_failures_total{namespace, claim}` | counter | pushes to the remote that failed |
| git-csi-driver | `git_csi_skipped_files{namespace, claim}` | gauge | files the last commit left out, over `commit.maxFileSize` |
| git-csi-driver | `git_csi_diverged{namespace, claim}` | gauge | one while the volume pushes to its side branch, zero while it pushes to its ref |
| git-csi-driver | `git_csi_volume_abnormal{namespace, volume}` | gauge | one while the volume's report says something is wrong with it, zero while it says nothing is |
| git-csi-driver | `git_csi_demanded_pulls_total{namespace, volume}` | counter | pulls a demand on the volume's `PersistentVolume` started |
| git-csi-driver | `git_csi_webhook_requests_total{result}` | counter | webhook requests the controller answered, by what it answered |
| git-csi-driver | `git_csi_webhook_marked_total` | counter | `PersistentVolumes` a verified push marked |

The guides for [writeable volumes](../../guides/writeable/) and
[read-only volumes](../../guides/read-only/) describe several of these
metrics in context.
