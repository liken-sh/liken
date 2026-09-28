# 04, Writeable volumes, unarmed

Built, and drilled in the lab on 2026-09-05 with development build
`0000.00.00-000-dev-012-ab33d059`. A pod wrote three files; the
condition, the metrics, and the events named them pending and the
volume unarmed; a `ReadWriteOnce` claim was refused with the mode in
its events; a pod deleted and made again found its files; a class on
the claim armed the volume. The first drill found that the kubelet
sends the `ReadWriteOncePod` access mode only to a driver that declares
`SINGLE_NODE_MULTI_WRITER`, and the driver declares it now. The record
file this plan added closes the open problem on a driver that forgets
its volumes when it restarts.

## The problem

An application wants a directory it can write, and a person wants that
directory to become a repository. Before the driver commits anything, it
has to hold the tree correctly, refuse the access modes that break the
single-writer promise, and report what it would commit.

## The design

### The volume

The volume is a static `PersistentVolume` with the `csi` block from the
design: `driver: git.liken.sh`, a `volumeHandle` unique in the cluster,
`volumeAttributes` with `url` and `ref`, and `nodePublishSecretRef`
when the repository needs a credential. `accessModes` is
`[ReadWriteOncePod]`. A claim names the volume with `volumeName`.

The kubelet stages a persistent volume before it publishes it, so the
lifecycle is `NodeStageVolume`, `NodePublishVolume`,
`NodeUnpublishVolume`, `NodeUnstageVolume`. The stage call carries the
access mode. The driver refuses, with `InvalidArgument`, a stage under
any access mode other than `SINGLE_NODE_SINGLE_WRITER`, and a stage
whose attributes carry a read-only attribute (`pull`, `depth`,
`offline`), because a writeable volume follows upstream only at stage.

### The store

A writeable volume has a work tree at `<store>/volumes/<volume
handle>/tree` and a git directory beside it at `<store>/volumes/<volume
handle>/git`, linked to the bare repository of its URL as an alternate,
so objects are shared and history is not cloned twice. The work tree
has no `.git` entry: git runs with `--git-dir` and `--work-tree`, and
the tree is what the pod sees.

Stage fetches the ref, creates the work tree from it when the volume
has never been staged on this node, and otherwise leaves the tree as
the last unpublish left it. The reconcile of a tree that is behind or
diverged is plan 06; in this plan a stage that finds upstream moved
leaves the tree alone and marks the condition abnormal with the two
commits.

Publish bind-mounts the work tree read-write onto the target path.
Unpublish unmounts it. Unstage leaves the work tree in place.

### The watch

After publish, the driver watches the work tree with inotify,
recursively, adding a watch for each directory created, and runs `git
status --porcelain` on a timer as the backstop, every minute by default.
A write, a create, a delete, or a rename starts the quiesce timer; the
default before a class sets it is thirty seconds. When the timer fires,
the driver runs `git status --porcelain -z` and records the pending
paths and their sizes as the volume's pending set.

An unarmed volume commits nothing. Its pending set is reported and
kept until a class arms it.

### Armed and unarmed

A volume is unarmed until its claim names a `VolumeAttributesClass`
whose `driverName` is `git.liken.sh`. The node plugin learns the claim
from the `PersistentVolume`: the kubelet passes `volume_id`, which is
the `volumeHandle`, and the driver lists `PersistentVolume` objects
with that handle to find `spec.claimRef`. It watches the claims it has
found for `spec.volumeAttributesClassName` and
`status.currentVolumeAttributesClassName`, and reads the class. This
plan only records the armed state and the class name; plan 05 acts on
it.

This needs `get`, `list`, and `watch` on `persistentvolumes`,
`persistentvolumeclaims`, and `volumeattributesclasses`, which
`rbac.yaml` adds to the `ClusterRole`.

### What survives a restart

The node plugin holds its published volumes in memory, and a restarted
driver knows none of them. This plan gives every volume, read-only and
writeable, a record file in its store directory, written at publish
with the attributes, the target path, and the staging path where one
exists, and removed at unpublish. At start the driver reads every
record, checks that the target path is still a mount, and resumes the
watch and the fetch loop for each one that is. A record whose target is
no longer mounted is removed with its directory, for a read-only
volume, or left in place, for a writeable one, because its work tree
may hold work.

### Reporting

The three channels carry the same facts:

- `VolumeCondition` is abnormal for an unarmed volume with a
  non-empty pending set (`unarmed: N paths pending, no class on claim
  <namespace>/<name>`), and for an upstream that moved at stage.
- An `Event` on the pod and on the claim when the volume becomes armed
  or unarmed, and when the pending set first becomes non-empty.
- Metrics from the design: `git_csi_armed` and a
  `git_csi_pending_paths` gauge, labeled `namespace` and `claim`, on a
  `/metrics` listener the `DaemonSet` exposes on a host port only to the
  node. Plan 05 adds the rest.

## Considered and set aside

- **Committing without a class.** An unarmed first commit is where a
  secret or a database gets pushed. The class is the consent step.
- **Reading the class from an annotation.** A string map with no
  validation and no status. The class exists for this.
- **A work tree with its own `.git` directory inside.** The pod would
  see it and could commit and push around the driver.

## Proof

- Unit tests run against real repositories and real directories, with a
  fake clientset from `k8s.io/client-go/kubernetes/fake` for the claim
  watch, because a real API server is the lab's job.
- In the lab, `lab/drills/04.sh`: a `PersistentVolume` against `hello`
  and a claim; a pod writes three files; the condition names three
  pending paths; a claim with `ReadWriteOnce` is refused with the
  message in the pod's events; the pod is deleted and recreated and
  finds its three files; the claim is patched to name a class and the
  condition says armed.
