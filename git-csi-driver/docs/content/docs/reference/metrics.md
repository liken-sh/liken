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
| git-csi-driver | `gitcsi_volumes{repo}` | gauge | what is mounted |
| git-csi-driver | `gitcsi_fetch_duration_seconds{repo}` | histogram | the forge is slow |
| git-csi-driver | `gitcsi_fetch_failures_total{repo}` | counter | the forge is down |
| git-csi-driver | `gitcsi_store_bytes` | gauge | growth on the wrong filesystem |

## The series that predate the contract

These keep the `git_csi_` prefix they shipped with. The guides for
[writeable volumes](../../guides/writeable/) and
[read-only volumes](../../guides/read-only/) say when each one moves.

| Metric | Labels | Meaning |
| --- | --- | --- |
| `git_csi_volume_abnormal` | `namespace`, `volume` | One while the volume's report says something is wrong with it, zero while it says nothing is. |
| `git_csi_demanded_pulls_total` | `namespace`, `volume` | Pulls a demand on the volume's `PersistentVolume` started. |
| `git_csi_armed` | `namespace`, `claim` | One when a class of the driver arms the volume, zero when none does. |
| `git_csi_pending_paths` | `namespace`, `claim` | Paths the last scan found that the driver has not committed. |
| `git_csi_unpushed_commits` | `namespace`, `claim` | Commits the work tree holds that the remote does not. |
| `git_csi_last_push_timestamp_seconds` | `namespace`, `claim` | When a push to the remote last worked, in seconds since the epoch. |
| `git_csi_push_failures_total` | `namespace`, `claim` | Pushes to the remote that failed. |
| `git_csi_skipped_files` | `namespace`, `claim` | Files the last commit left out, over `commit.maxFileSize`. |
| `git_csi_diverged` | `namespace`, `claim` | One while the volume pushes to its side branch, zero while it pushes to its ref. |
| `git_csi_webhook_requests_total` | `result` | Webhook requests the controller answered, by what it answered. |
| `git_csi_webhook_marked_total` | | `PersistentVolumes` a verified push marked. |
