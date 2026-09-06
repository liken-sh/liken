# 07, Read-only claims

Built, and drilled in the lab on 2026-09-06 with development build
`0000.00.00-000-dev-017-065b9986`. Two pods read a `ReadOnlyMany` claim;
a push reached both inside a 15 second `pull`; a write in a pod failed
on a read-only file system, and a pod that did not ask for read-only
was refused with `GitVolumeRefused`; the driver's node pod was deleted,
and the new one took the volume back and followed a second push; the
last pod's exit removed the tree from the store; with the forge stopped,
a claim with `offline: refuse` carried `GitVolumeRefused`, and the
forge's return started its pod. The first run of the drill found the
read-only rule: the container runtime binds a claim into a pod
read-write unless the pod asks, whatever the driver's bind says. The
same build serves the franchises library on a real cluster.

## The problem

A workload that names its storage as a `PersistentVolumeClaim` cannot
mount an inline volume. The library operator is one: a `Library` names
a claim, its scan Jobs mount that claim, and its screen pods mount the
same claim to read the art beside the titles. Today the operator carries
git of its own to read a franchises repository, because this driver
serves a read-only repository through an inline volume alone, and a
claim on a repository is refused at stage as not `ReadWriteOncePod`.

Several pods may hold such a claim at once, on one node or on many,
and none of them writes.

## The design

### The volume

A read-only claim is a static `PersistentVolume` with the access mode
`ReadOnlyMany`, a `csi` block that names this driver, and the same
attributes an inline volume takes. The claim binds with the same access
mode. Neither names a `VolumeAttributesClass`, because there is no
policy to change: a read-only volume commits nothing and pushes
nothing.

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: franchises
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadOnlyMany]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: ""
  csi:
    driver: git.liken.sh
    volumeHandle: franchises
    readOnly: true
    volumeAttributes:
      url: https://tangled.org/guid.foo/fiction-franchises
      ref: main
      pull: 5m
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: franchises
  namespace: default
spec:
  accessModes: [ReadOnlyMany]
  storageClassName: ""
  volumeName: franchises
  resources: {requests: {storage: 1Gi}}
```

| Attribute | Values | Default |
|---|---|---|
| `url` | Any URL git accepts. Required. | none |
| `ref` | A branch or tag name. | `main` |
| `pull` | A Go duration, or `never`. | `5m` |
| `depth` | A positive integer, or `0` for a full clone. | `0` |
| `offline` | `refuse` or `allowStale`. | `refuse` |

A private repository names a `Secret` through `nodeStageSecretRef`,
with the same keys the inline form takes through
`nodePublishSecretRef`.

### The access mode decides the kind

The kubelet sends the stage call with the access mode the
`PersistentVolume` declares. `SINGLE_NODE_SINGLE_WRITER`, which is
`ReadWriteOncePod`, stages a writeable volume as plan 04 states.
`MULTI_NODE_READER_ONLY`, which is `ReadOnlyMany`, and
`SINGLE_NODE_READER_ONLY` stage a read-only volume. Every other mode is
refused with `InvalidArgument` and a message that names the two modes
the driver serves.

A read-only stage refuses the attributes a writeable volume alone
takes, the way an inline publish refuses them today, and a writeable
stage keeps refusing `pull`, `depth`, and `offline`.

### What a read-only stage does

A read-only stage places the ref in `<store>/volumes/<volume id>/tree`
from the shared bare repository, exactly as an inline publish does, and
adds the tree to the follower with the volume's `pull`. It makes no
work tree, arms nothing, and watches nothing. `offline` means what it
means for an inline volume: `refuse` fails the stage when the fetch
fails, and `allowStale` stages what the store holds and reports the
failure.

A publish of a staged read-only volume bind-mounts the tree read-only
onto the pod's target path. The pod has to ask for read-only, with
`readOnly: true` on its `persistentVolumeClaim` volume, because the
container runtime binds the target into the pod read-write otherwise,
whatever the driver's own bind says. A publish that does not ask is
refused with `InvalidArgument`, the way an inline volume without
`readOnly: true` is refused. Many pods on one node publish the same
staged volume, each at its own target, and the driver keeps every
target it bound. An unpublish removes one target. The unstage, which
the kubelet sends after the last unpublish on the node, removes the
tree from the follower and deletes the volume's directory.

The record a read-only staged volume writes carries every bound
target and the staging path, so a driver that restarts resumes the
follower for the tree and reports on the targets that are still
mounted. The driver mounts nothing at a staging path, so the targets
are the whole evidence: a record with no target the kernel still holds
is dropped, and its tree with it.

### Events and the gauge

A read-only claim reports where an inline volume reports, and on the
claim as well. `GitVolumeStale`, `GitFetchFailed`, and
`GitVolumeRefused` are posted on every pod the volume is published to
and on the `PersistentVolumeClaim` the volume handle is bound to, which
the driver finds the way an armed volume finds its claim. The gauge
`git_csi_volume_abnormal` carries the claim's namespace and the volume
handle, so a claim that cannot reach its forge reads `1` under its own
name.

### Considered and set aside

- **A `ReadOnlyMany` claim that names a class.** The driver ignores a
  class on a read-only claim and reads nothing from it. Refusing it
  would need the stage call to know the claim's class, which it does
  not carry, and a class does nothing here, so it is documented as
  ignored rather than read for a refusal.
- **A shared tree per URL and ref on the node.** Two claims on one ref
  would share one checkout. The store keeps one tree per volume handle
  instead, because two claims may carry two `pull` settings and two
  `offline` settings, and a tree of one handle is what the record and
  the gauge name.

## Proof

Drill 07 in the lab, `lab/drills/07.sh`:

1. Apply a `ReadOnlyMany` `PersistentVolume` and claim on the forge's
   repository with a 15 second `pull`, and two pods in one namespace
   that mount the claim. Both pods start, and both read the greeting.
2. Push to the forge. Both pods read the new greeting inside `pull`.
3. Write in one pod. The write fails with a read-only file system. A
   third pod that mounts the claim without `readOnly: true` is refused
   with `GitVolumeRefused` and never runs.
4. Delete the driver's node pod. The two pods keep their mounts, the
   new driver pod resumes the follower, and a second push reaches
   both pods.
5. Delete both pods. The node store holds no tree for the handle.
6. Stop the forge and start a pod on a second claim with
   `offline: refuse`. The pod stays `ContainerCreating` and the claim
   carries `GitVolumeRefused`. Start the forge. The pod starts.

The drill passes on the `liken` guest, and `make test` holds at 100.
