# 16, The reclaim policy decides

Built and drilled on liken-1 on 2026-10-10, with the development
build `2026.10.10-001-dev-007-7a36a680`. The last section gives what
the drill measured. This plan closes the open problem "A kept work tree
with no volume".

## The problem

A writeable volume keeps its work tree on each node that staged it.
The node plugin removed a tree on an age rule: an hourly sweep took a
tree that no volume held, whose last unstage was older than
`--sweep-after`, 30 days by default, and whose every commit the last
push sent. A tree with an unpushed commit, or a diverged tree, stayed
for good. When its remote was gone too, no person could remove it
without a shell on the node, and `liken` gives none.

Kubernetes already has a rule for the data of a deleted volume, and
the driver ignored it. A `PersistentVolume` names a
`persistentVolumeReclaimPolicy`: `Retain` keeps the data after the
volume is deleted, and `Delete` removes it. The driver deployed no
`external-provisioner` and declared no `CREATE_DELETE_VOLUME`, so a
released `PersistentVolume` of the `Delete` policy stayed `Released`
with no `Event`. On liken-1, a static `PersistentVolume` named
`repro-git-delete`, with the `Delete` policy, stayed `Released` with no
`Event` after its claim was deleted.

## The design

### The policy decides, as it does for every volume

The data of a git volume is its work trees, never the remote
repository. So `Retain` keeps every tree, for good, and `Delete`
removes every tree when the `PersistentVolume` is deleted, with any
commit in it that no push sent. The unstage pushes before the volume
leaves the node, so `Delete` loses only a commit whose push failed.
The age rule, the `unstaged` file, the `--sweep-after` flag, and the
report of a kept tree in the next volume of the same repository go.
The `GitVolumeSwept` and `GitVolumeAbandonedWork` `Event`s go with
them.

### The upstream sidecar deletes the PersistentVolume

The controller `Deployment` runs `csi-provisioner` v6.3.0 beside the
resizer, and the controller declares `CREATE_DELETE_VOLUME`.
`DeleteVolume` answers success and removes nothing, because no tree is
on the controller's node. The sidecar then deletes the
`PersistentVolume`. The sidecar treats a `PersistentVolume` with no
`pv.kubernetes.io/provisioned-by` annotation as its own when
`spec.csi.driver` names its driver, so a person writes no annotation.
It also adds `external-provisioner.volume.kubernetes.io/finalizer` to
a bound `PersistentVolume` of the `Delete` policy, so a person who
deletes the `PersistentVolume` before the claim still reaches
`DeleteVolume`. Its retries, its `Event`s, and its leader election are
upstream's. `CreateVolume` still refuses, and the sidecar posts that
refusal on a claim whose `StorageClass` names `git.liken.sh`.

The alternative was a watch in the controller that deletes released
`PersistentVolume`s itself. It needs no sidecar, and it repeats what
upstream already maintains and tests.

### Each node removes its own tree

The node plugin already watches every `PersistentVolume`. When the
watch reports a delete of one of the driver's, the node removes its
tree of that handle when three things are true: the node holds no
stage of it, no other `PersistentVolume` carries the handle, and the
last policy the node recorded for it is `Delete`. An unstage checks
the same three things, because the delete can arrive while the kubelet
still unstages the volume.

A node plugin that was down during the delete never sees it. Its first
read of the cluster after it starts finds no `PersistentVolume` that
carries the handle, and that read is the evidence. The
`PersistentVolume` is gone by then, so the node records the policy in
a `reclaim-policy` file in the volume's directory, at each stage and
at each change the watch reports. The node removes nothing until the
watch's first read is in its store, because an empty store says
nothing about a delete. A tree with no recorded policy, such as one an
earlier driver left, is kept.

### The repository walk stays

The hourly walk of the bare repositories stays, and runs after each
tree the node removes too. A read-only volume leaves the store at
every unstage, and nothing reports that to the repository it read
from, so the walk finds a repository that no volume names. The walk's
`git gc` takes git's own prune age, `gc.pruneExpire`, two weeks by
default, in place of `--sweep-after`. Each fetch already runs `git
maintenance run --auto`, which prunes at that same age.

## Proof

The tests run in `synctest` bubbles against a fake API server:

- A deleted `PersistentVolume` of the `Delete` policy takes its tree
  and its bare repository, and one of the `Retain` policy keeps both.
  A policy changed after the stage decides, in both directions.
- A tree the node still stages stays through the delete, and goes at
  the unstage, after the unstage pushed the last write.
- A second `PersistentVolume` that carries the same handle keeps the
  tree.
- A node plugin whose watch was down during the delete removes a
  `Delete` tree at its next start, and keeps a `Retain` tree and a tree
  with no recorded policy.
- The controller declares `MODIFY_VOLUME` and `CREATE_DELETE_VOLUME`,
  answers `DeleteVolume`, and refuses one that names no volume.

## The drill

On liken-1, with a writeable volume of `https://github.com/liken-sh/.github.git`
staged by a pod on the node `liken-1`, unarmed so that nothing pushed.
A pod with the store's directory mounted read-only listed the store.

1. `repro-git-delete`, a `PersistentVolume` of the `Delete` policy
   that had stayed `Released` with no `Event` since its claim was
   deleted, was deleted within a second of the new provisioner taking
   its `Lease`.
2. A bound `PersistentVolume` of the `Delete` policy carried the
   provisioner's finalizer, and the `Retain` one did not. The volume's
   directory recorded `Delete`. One second after the claim was
   deleted, the `PersistentVolume` was gone, the node logged
   `removed the work tree ... unpushed=false`, and the walk removed the
   bare repository.
3. Under `Retain`, the `PersistentVolume` stayed `Released` after its
   claim was deleted. After a person deleted it, the tree and its bare
   repository stayed.
4. With the node plugin stopped by a `nodeSelector` that matched no
   node, the claim of a `Delete` volume was deleted, and the
   provisioner deleted its `PersistentVolume`. The tree stayed while
   the plugin was down. The plugin removed it at its next start, and
   the `Retain` tree stayed through the restart.
5. A new `PersistentVolume` with the `Retain` tree's handle staged
   from that tree: the tree's `git/HEAD` kept its time from the first
   stage. A change of its policy to `Delete` was recorded in the
   volume's directory within three seconds, and the release removed
   the tree and the repository.

The drill did not lose a commit to `Delete`, because the remote took
every push. The tests cover a tree with an unpushed commit.
