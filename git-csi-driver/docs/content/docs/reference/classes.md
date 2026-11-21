---
title: Class parameters
weight: 20
---

# Class parameters

A `VolumeAttributesClass` with `driverName: git.liken.sh` sets the
commit and push policy of a writeable volume in `parameters`. Every
parameter has a default, so an empty class works. The controller
rejects a class with an unknown or malformed parameter, and the error
message gives the first bad one.

| Parameter | Values | Default |
|---|---|---|
| `push.quiesce` | A Go duration, at least `5s`. The driver commits and pushes after the tree has been quiet this long. | `30s` |
| `push.maxLatency` | A Go duration, at least `push.quiesce`, or `never`. The driver pushes when the oldest unpushed commit is this old, whatever the tree is doing. | `5m`, or `push.quiesce` when that is longer |
| `commit.maxFileSize` | A Kubernetes quantity such as `1Mi`, or `0` for no limit. A larger file is not committed and is named in the events and the log. | `1Mi` |
| `commit.author` | `Name <email>`. The author and committer of every commit. | `git-csi-driver <git-csi-driver@liken.sh>` |
| `ignore` | `.gitignore` patterns, added to the repository's own. Separate them with commas, or put one on each line with a YAML block scalar. | empty |
| `metadata` | `true` or `false`. Whether modes, owners, and empty directories are recorded on `refs/git-csi/metadata` and replayed at restore. | `true` |

`paths` is not a class parameter. It is a volume attribute, set in
the `csi` block of the `PersistentVolume`, which cannot change.
[The paths attribute](../attributes/#the-paths-attribute) describes
it. The controller rejects a class that sets `paths`, as it
rejects every unknown parameter.

## Events

| Reason | When |
|---|---|
| `GitVolumeArmed` | The claim now uses a class of this driver, so the driver starts committing and pushing. |
| `GitVolumeUnarmed` | The claim no longer uses a class of this driver, so the driver stops committing. |
| `GitVolumePending` | The tree has paths that the driver has not committed. Posted when the first such path appears. |
| `GitVolumePushed` | A push succeeded, with the commit and the count of paths. |
| `GitVolumePushFailed` | A push failed, with git's error. |
| `GitVolumeFileSkipped` | A file over `commit.maxFileSize` was left out of a commit, or the pod wrote a file outside the volume's `paths` for the first time. The driver never commits a file outside the `paths`. It sees those writes only through its inotify watch of the tree, so it posts no event for a write that the watch did not see, such as a write made while the driver was not running. |
| `GitVolumeRebased` | A push was rejected, so the driver rebased the tree onto upstream and pushed again, with the count of commits and the upstream commit. |
| `GitVolumeDiverged` | The volume moved to its side branch, because a push was still rejected after three rebases, a rebase aborted, or the application and upstream both changed a file. |
| `GitVolumeHealed` | A person merged the side branch into the ref, so the volume pushes to its ref again and the driver deletes the side branch. |
| `GitVolumeNoPublishSecret` | The `PersistentVolume` sets `nodeStageSecretRef` and no `nodePublishSecretRef`. The driver keeps the credential only in memory, so after a restart of the driver the volume cannot fetch or push until the kubelet stages it again. Posted once for each volume each time the driver runs. |
| `GitVolumeUpstreamMoved` | When the pod started, upstream had moved while the tree had uncommitted writes, so the driver left the tree as it was. |
| `GitVolumeRefDeleted` | When the pod started, the remote no longer had the ref, so the driver pushes nothing until the ref exists again. |

For a failed push, a failed fetch, or an upstream that moved, the
driver posts the event at the first failure after a success. It does
not post it again until a success ends that fault. A fault of one kind
does not stop the event of another kind.

## The abnormal gauge

`git_csi_volume_abnormal{namespace, volume}` is one while any of these
is true for a volume, and zero otherwise. The `volume` label is the
`volumeHandle`.

* The volume lost its credential at a restart of the driver.
* The last fetch or the last push failed.
* Upstream moved while the tree had uncommitted writes.
* The remote no longer has the ref.
* The volume pushes to its side branch.
* The claim's class is invalid.
* The last commit left out a file over `commit.maxFileSize`.
* The commit of a read-only volume's ref has no file or directory at
  an entry of `paths`.
* An unpushed commit is older than `push.maxLatency`.
* The claim has no class of this driver, and the tree has changes that
  are not committed.

A file that the pod wrote outside the volume's `paths` counts in
`git_csi_skipped_files`, and it does not make the volume abnormal. The
driver's log gives the reason for each abnormal volume.

## Metrics

[What the driver reports](../metrics/) lists the metrics of each
writeable volume, with the rest of the driver's metrics.
