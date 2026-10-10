---
title: Class parameters
weight: 20
---

# Class parameters

A `VolumeAttributesClass` with `driverName: git.liken.sh` carries a
commit and push policy in `parameters`. Every parameter has a default,
so an empty class arms a volume with the defaults. The controller
refuses a class with an unknown or malformed parameter, naming the
first bad one.

| Parameter | Values | Default |
|---|---|---|
| `push.quiesce` | A Go duration, at least `5s`. The driver commits and pushes after the tree has been quiet this long. | `30s` |
| `push.maxLatency` | A Go duration, at least `push.quiesce`, or `never`. The driver pushes when the oldest unpushed commit is this old, whatever the tree is doing. | `5m`, or `push.quiesce` when that is longer |
| `commit.maxFileSize` | A Kubernetes quantity such as `1Mi`, or `0` for no limit. A larger file is not committed and is named in the events and the log. | `1Mi` |
| `commit.author` | `Name <email>`. The author and committer of every commit. | `git-csi-driver <git-csi-driver@liken.sh>` |
| `ignore` | Comma-separated `.gitignore` patterns, added to the repository's own. | empty |
| `metadata` | `true` or `false`. Whether modes, owners, and empty directories are recorded on `refs/git-csi/metadata` and replayed at restore. | `true` |

## Events

| Reason | When |
|---|---|
| `GitVolumeArmed` | The claim named a class of this driver. |
| `GitVolumeUnarmed` | The claim names no class of this driver. |
| `GitVolumePending` | The tree holds paths the driver has not committed. Posted when the set first becomes non-empty. |
| `GitVolumePushed` | A push succeeded, with the commit and the count of paths. |
| `GitVolumePushFailed` | A push failed, with git's error. |
| `GitVolumeFileSkipped` | A file over `commit.maxFileSize` was left out of a commit. |
| `GitVolumeRebased` | A rejected push rebased the tree onto upstream and landed, with the count of commits and the upstream commit. |
| `GitVolumeDiverged` | The volume moved to its side branch, after a push still rejected after three rebases, an aborted rebase, or a file the application and upstream both changed. |
| `GitVolumeHealed` | Upstream took the side branch's work, and the volume is back on its ref. |
| `GitVolumeNoPublishSecret` | The `PersistentVolume` names `nodeStageSecretRef` and no `nodePublishSecretRef`, so a restart of the driver loses the credential. Posted once for each stage and for each restart. |
| `GitVolumeUpstreamMoved` | The stage found that upstream moved while the tree held uncommitted writes, so it left the tree as it was. Posted on the claim when the driver finds it after the stage. |
| `GitVolumeRefDeleted` | The stage found that the remote no longer holds the ref, so the driver pushes nothing until the ref exists again. Posted on the claim when the driver finds it after the stage. |

Each fault, a failed push, a failed fetch, and an upstream that moved,
posts its event at the first failure after a success, and does not post
it again until a success ends that fault. A fault of one kind does not
hold back the event of a fault of another kind.

## The abnormal gauge

`git_csi_volume_abnormal`, labeled `namespace` and `volume`, is one for
an unarmed volume with pending paths, a failed push, a skipped file, an
unpushed commit older than `push.maxLatency`, an invalid class, a ref
that moved upstream while the tree held uncommitted writes, a diverged
volume, and a ref the remote no longer holds. The driver's log says
which.

## Metrics

Each gauge is labeled `namespace` and `claim`.

| Metric | Meaning |
|---|---|
| `git_csi_armed` | One when a class of this driver arms the volume. |
| `git_csi_pending_paths` | Paths the last scan found that the driver has not committed. |
| `git_csi_unpushed_commits` | Commits not yet on the remote. |
| `git_csi_last_push_timestamp_seconds` | When the last push succeeded. |
| `git_csi_push_failures_total` | Pushes that failed. |
| `git_csi_skipped_files` | Files over `commit.maxFileSize` in the tree. |
| `git_csi_diverged` | One while the volume pushes to its side branch. |
| `git_csi_volume_abnormal` | One while anything above is wrong. Labeled `namespace` and `volume`, not claim. |
