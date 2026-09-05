---
title: Volume attributes
weight: 10
---

A read-only volume is described by the `volumeAttributes` of an inline
CSI volume. The driver refuses an unknown attribute, a malformed value,
and an inline volume without `readOnly: true`, with a message that
names the attribute.

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

## The condition

`NodeGetVolumeStats` reports the tree's size as used and a
`VolumeCondition`. The condition is normal after a successful publish or
fetch and says the ref and the commit. It is abnormal, with the error,
after a stale publish and after a failed fetch, until a fetch succeeds.
The kubelet exposes it as `kubelet_volume_stats_health_status_abnormal`.
