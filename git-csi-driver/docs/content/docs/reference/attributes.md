---
title: Volume attributes
weight: 10
---

# Volume attributes

`volumeAttributes` configure a read-only volume. They go in the `csi`
block of an inline volume or of a `ReadOnlyMany` `PersistentVolume`. The
[read-only guide](../../guides/read-only/) shows how to use them. The
driver refuses a volume that has an unknown attribute, a malformed
value, or no `readOnly: true`, and the error message names the
attribute.

| Attribute | Values | Default |
|---|---|---|
| `url` | The repository to clone, as a URL that git accepts: `https://`, `ssh://`, `git://`, `file://`, or `user@host:path`. Required. | none |
| `ref` | The branch or tag to check out. | `main` |
| `pull` | A Go duration such as `5m` or `30s`, `on-demand`, or `never`. | `5m` |
| `depth` | The number of commits to fetch, as a whole number, or `0` for the full history. It applies only to the first fetch of the repository on a node. | `0` |
| `offline` | What to do when the fetch fails at pod start. `refuse` refuses the volume. `allowStale` mounts the node's last copy of the ref. | `refuse` |
| `webhookSecret` | The name of a `Secret` in the claim's namespace. The controller verifies a push from the forge against the `secret` key of this `Secret` before it marks this volume for a pull. | none |

`pull` takes one of three values.

| Value | Meaning |
|---|---|
| `never` | The volume never pulls and ignores demands. It keeps the commit it staged. |
| `on-demand` | The volume has no timer. It pulls only when something demands a pull. An inline volume refuses this value, because a demand is an annotation on a `PersistentVolume`, and an inline volume has no `PersistentVolume`. |
| A duration such as `5m` | The volume pulls on that interval, and it also pulls when something demands a pull. |

The driver fetches as root on the node, with a URL that comes from a
pod spec, so it refuses two kinds of URL before it runs git: a URL that
starts with a dash, which git would read as an option, and a URL of the
form `<transport>::<address>`, which makes git run a helper program.

## Access modes

The access mode of a `PersistentVolume` decides whether the driver
stages a writeable volume or a read-only claim. The driver refuses
every other access mode, and its error message lists the modes that it
accepts. An inline volume has no access mode and is always read-only.

| Access mode | What the driver stages |
|---|---|
| `ReadWriteOncePod` | A writeable volume. See the [writeable guide](../../guides/writeable/). |
| `ReadOnlyMany` | A read-only claim, which the driver publishes to every pod on the node that uses it. |

A read-only claim accepts `pull`, `depth`, `offline`, and
`webhookSecret`. A writeable volume refuses all four, because it
updates from its ref only at stage. A pod must set `readOnly: true` on
the `persistentVolumeClaim` volume that uses a read-only claim, or the
driver refuses the publish.

## Credentials

An inline volume names its `Secret` in `nodePublishSecretRef`, and the
`Secret` is in the pod's namespace. A `PersistentVolume` names the same
`Secret` in both `nodeStageSecretRef` and `nodePublishSecretRef`. The
stage fetches with the first. The kubelet sends the second with every
publish call, and again on each pod sync, which is how a restarted
driver gets the credential back. The driver refuses a publish whose
`Secret` differs from the stage's. The driver reads these keys:

| Key | Use |
|---|---|
| `ssh-privatekey` | The private key for an SSH URL. |
| `known_hosts` | The host keys that ssh trusts, in the `known_hosts` format. With it, the host key must match. Without it, ssh accepts the first host key that it sees and refuses a later change. |
| `token` | The password for an HTTPS URL. |
| `username` | The user name to send with the token. Default `git`. |

The driver refuses a `Secret` that has neither `ssh-privatekey` nor
`token`.

## Events

| Reason | When |
|---|---|
| `GitVolumeRefused` | The driver refused to publish the volume. The message gives the reason. |
| `GitVolumeStale` | The fetch failed, and `offline: allowStale` made the driver mount the node's last copy. |
| `GitFetchFailed` | A fetch failed after one that worked. The driver posts it once, until a fetch succeeds. |
| `GitFetchRecovered` | A fetch succeeded after a `GitFetchFailed` or a `GitVolumeStale`. It is a `Normal` event, and it shows when the fault ended. |
| `GitVolumeNoPublishSecret` | The `PersistentVolume` has `nodeStageSecretRef` and no `nodePublishSecretRef`, so the driver loses the credential when it restarts. The driver posts it once for each stage and once for each restart. |

When the driver posts the same event with the same message on the same
object again, it increases the count of the existing event instead of
creating a new one. The kubelet retries a refused mount with a backoff
that grows to about two minutes, so a pod that is refused for an hour
shows one `GitVolumeRefused` line, such as `(x37 over 1h)`. The API
server deletes an event one hour after its last change.

## The gauge

The gauge `git_csi_volume_abnormal`, with the labels `namespace` and
`volume`, is 1 after a stale publish and after a failed fetch, until a
fetch succeeds. The driver's log says what went wrong when the gauge
rises and when it falls. The CSI call `NodeGetVolumeStats` reports the
size of the tree as the space used.
