---
title: Volume attributes
weight: 10
---

A read-only volume is described by its `volumeAttributes`, in the `csi`
block of an inline volume or of a `ReadOnlyMany` `PersistentVolume`. The
driver refuses an unknown attribute, a malformed value, and a volume
without `readOnly: true`, with a message that names the attribute.

| Attribute | Values | Default |
|---|---|---|
| `url` | A URL git accepts: `https://`, `ssh://`, `git://`, `file://`, or `user@host:path`. Required. | none |
| `ref` | A branch or tag name. | `main` |
| `pull` | A Go duration such as `5m` or `30s`, or `never`. | `5m` |
| `depth` | A whole number of commits, or `0` for a full clone. It applies to the first fetch of a repository on a node. | `0` |
| `offline` | `refuse` or `allowStale`. | `refuse` |

Two URL shapes are refused before git sees them. A URL that starts with
a dash reads as an option to git. A URL of the form
`<transport>::<address>` names a helper program git runs. The driver
fetches as root on the node and the URL comes from a pod spec, so
neither is accepted.

## Access modes

The access mode of a `PersistentVolume` decides what the driver stages.
The kubelet sends `ReadWriteOncePod` as the CSI mode
`SINGLE_NODE_SINGLE_WRITER`, and `ReadOnlyMany` as
`MULTI_NODE_READER_ONLY`. The driver also stages a read-only claim under
`SINGLE_NODE_READER_ONLY`, which the kubelet does not send. It refuses
every other mode, and the message names the modes it serves. An inline
volume carries no access mode and is always read-only.

| Access mode | What the driver stages |
|---|---|
| `ReadWriteOncePod` | A writeable volume. |
| `ReadOnlyMany` | A read-only claim, published to every pod on the node that mounts it. |

A read-only claim takes `pull`, `depth`, and `offline`. A writeable
volume refuses all three, because it follows its ref at stage alone. A
read-only claim names its `Secret` through `nodeStageSecretRef`, and a
pod mounts it with `readOnly: true` on its `persistentVolumeClaim`
volume, or the publish is refused.

## Credentials

`nodePublishSecretRef` names a `Secret` in the pod's namespace. The
driver reads these keys:

| Key | Use |
|---|---|
| `ssh-privatekey` | The private key for an SSH URL. |
| `known_hosts` | The host keys ssh accepts. With it, the host key must match. Without it, ssh accepts the first key and refuses a change. |
| `token` | The password for an HTTPS URL. |
| `username` | The user for the token. Default `git`. |

A `Secret` with neither `ssh-privatekey` nor `token` is refused.

## Events

| Reason | When |
|---|---|
| `GitVolumeRefused` | The publish was refused. The message says why. |
| `GitVolumeStale` | The fetch failed and `offline: allowStale` published the node's copy. |
| `GitFetchFailed` | A fetch failed after one that worked. Posted once, until a fetch succeeds. |

## The gauge

`git_csi_volume_abnormal`, labeled `namespace` and `volume`, is one
after a stale publish and after a failed fetch, until a fetch succeeds.
The driver's log says what went wrong when the gauge rises and when it
falls. `NodeGetVolumeStats` reports the tree's size as used.
