---
title: Give an application a repository to write
weight: 30
description: "Give an application a git repository as a writeable volume that the driver commits and pushes. Use when an application writes files that must go into git, when upstream moves, when several writers share one repository, or to restore a volume."
---

# Give an application a repository to write

A writeable volume lets an application keep its files in a git
repository. It takes three objects: a `PersistentVolume` that names the
repository, a `PersistentVolumeClaim` that binds it, and a
`VolumeAttributesClass` that sets how the driver commits and pushes. The
application sees a plain directory and writes to it as usual. The driver
commits what the application wrote and pushes the commits.

## The volume

The `PersistentVolume` sets the repository, the ref, and the
credentials. Its `csi` block cannot change after you create it, so the
settings that you may want to change later go in the class, below.

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: homeassistant-config
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadWriteOncePod]
  persistentVolumeReclaimPolicy: Retain
  csi:
    driver: git.liken.sh
    volumeHandle: homeassistant-config
    volumeAttributes:
      url: git@code.example.com:home/homeassistant.git
      ref: main
    nodeStageSecretRef:
      name: homeassistant-deploy-key
      namespace: home
    nodePublishSecretRef:
      name: homeassistant-deploy-key
      namespace: home
```

The Kubernetes API requires `capacity`, and the driver ignores it.
With `Retain`, the node keeps its work tree when you delete the
`PersistentVolume`. With `Delete`, the node removes it. [Work trees the
node keeps](#work-trees-the-node-keeps) says when to choose each. The
access mode must be `ReadWriteOncePod`. The driver refuses
`ReadWriteOnce`, because it lets two pods on one node write the same
tree.

A private repository needs the same `Secret` in both references.
[The credential](#the-credential) gives the reason.

## The claim

The claim names the volume in `volumeName`. Until the claim also names
a class, the volume is unarmed: the driver watches the tree and reports
what it would commit, and commits nothing. Add the repository's
`.gitignore` now, before the first commit can include a token or a
database.

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: config
  namespace: home
spec:
  volumeName: homeassistant-config
  accessModes: [ReadWriteOncePod]
  resources: {requests: {storage: 1Gi}}
```

Mount the claim in the application's pod like any other claim. Set
`strategy: Recreate` on a `Deployment`. A rolling update starts the new
pod before it stops the old one, and `ReadWriteOncePod` does not let
the new pod start while the old pod uses the claim, so the update never
finishes.

## The class

A `VolumeAttributesClass` sets the commit and push policy of a volume.
Name it in the claim to arm the volume: an armed volume is one that
the driver commits and pushes. Because the claim's class is
mutable, you can change the policy without restarting the application.

```yaml
apiVersion: storage.k8s.io/v1
kind: VolumeAttributesClass
metadata:
  name: config-eager
driverName: git.liken.sh
parameters:
  push.quiesce: 30s
  push.maxLatency: 5m
  commit.maxFileSize: 1Mi
  commit.author: Home Assistant <homeassistant@home.example>
  ignore: ".storage/,*.db*,*.log"
```

```console
kubectl patch pvc config -n home -p '{"spec":{"volumeAttributesClassName":"config-eager"}}'
```

Set the class after the claim is bound. Kubernetes binds a claim to a
static volume only when both name the same class. So a claim that names
a class before it binds needs the same `volumeAttributesClassName` on
the `PersistentVolume`. A claim that is already bound accepts a class
change without that.

The [class reference](../../reference/classes/) lists every parameter,
its values, and its default.

## The credential

The kubelet reads the volume's `Secret` and sends it to the driver. The
stage call sends `nodeStageSecretRef`, and the driver fetches the ref
with it before the pod starts. Each publish call sends
`nodePublishSecretRef`, and the kubelet calls publish again for every
mounted volume on each pod sync, about once a minute. The driver keeps
the credential in memory and writes it to a file on the node only while
a git command runs.

So after the driver restarts, for example in an upgrade, it has no
credential until the next publish call. Then it pushes the commits in
the tree, and the application does not restart. Until then, the
volume's report and the gauge `git_csi_volume_abnormal` show that the
volume is waiting for its credential.

A rotated `Secret` reaches the driver at the next publish call, and the
driver pushes every unpushed commit with the new key at once, instead
of waiting for the next timed push. The driver refuses a publish whose
`Secret` differs from the stage's, and posts `GitVolumeRefused`, because
one volume cannot use two credentials.

A `PersistentVolume` that has `nodeStageSecretRef` and no
`nodePublishSecretRef` works until the driver restarts. After that, the
driver cannot push for it until the pod is deleted and started again.
The driver posts `GitVolumeNoPublishSecret` on the pod and the claim
when it publishes such a volume. The `csi` block cannot change, so
replace the `PersistentVolume`, and do it before you upgrade the
driver, because an upgrade restarts it:

1. Scale the application to zero. The driver pushes the commits in the
   tree when the pod stops.
2. Delete the claim and then the `PersistentVolume`. With
   `persistentVolumeReclaimPolicy: Retain`, the repository and the
   node's work tree are kept.
3. Create the `PersistentVolume` again with the same `volumeHandle` and
   both references, and create the claim again.
4. Scale the application up. When the pod runs on the same node, the
   stage starts from the work tree that the node kept.

If the driver has already restarted, it has no credential at step 1,
so it cannot push when the pod stops, and it posts
`GitVolumePushFailed`. The commits stay in the node's work tree until
the volume is staged on the same node again.

## What happens after a write

The driver waits until the tree has had no writes for `push.quiesce`.
Then it commits every changed path that is not ignored and not larger
than `commit.maxFileSize`. It pushes when `push.quiesce` passes with no
new write, when the oldest unpushed commit is older than
`push.maxLatency`, and when the pod stops. A write made while the driver
was not running, for example during an upgrade, produces no file event
for the driver to see. So the driver also starts the wait for
`push.quiesce` when it starts to watch the tree, and commits that write
when the wait ends.

The driver records file modes, owners, and empty directories on a ref of
its own, `refs/git-csi/metadata`. That ref is not part of the tree and
does not show in the forge's file view.

## When upstream moves

The driver brings in upstream commits at stage, before the pod starts.
While the pod runs, it changes the tree's files only after a rejected
push, described after this list. At stage, the driver compares the tree
with the ref:

- **Behind.** The tree has no commits that upstream lacks, and upstream
  has new commits. The driver updates the tree to upstream.
- **Ahead.** The tree has commits that upstream lacks. Nothing changes,
  and the next push sends the commits.
- **Diverged.** Both have commits that the other lacks. The driver
  rebases the tree's commits onto upstream. When the rebase conflicts,
  the driver aborts it and moves the volume to a side branch.
- **Uncommitted writes.** The tree has writes that are not committed yet.
  The driver leaves the tree as it is, whatever upstream did, and the
  gauge `git_csi_volume_abnormal` and the log report that upstream
  moved.

When the forge rejects a push because the ref moved, the driver fetches,
rebases the tree's commits onto upstream in a scratch copy of the tree,
and pushes again, up to three times. Then it updates the pod's tree in
one step that rewrites only the files that upstream changed, and posts
`GitVolumeRebased` on the claim. When the application wrote a file since
the last commit and upstream also changed that file, git refuses to
update the tree, and the driver moves the volume to a side branch.

The driver moves the volume to the side branch `<ref>.<volumeHandle>`
when a push is still rejected after the third rebase, when a rebase
fails and is aborted, or when the application and upstream changed the same file.
Every push goes to that branch until a person merges it into the ref on
the forge. The driver keeps committing, so no work is lost, and the
events and the log name both branches. At the volume's next push after the merge, or at its
next pod start, the volume is back on the ref and the driver deletes the
side branch.

## Many writers on one repository

One repository can hold the configuration of many applications. Each
application has its own writeable volume, and its own directory of the
repository is mounted with `subPath`. [Give many applications one
repository](../one-repository-many-apps/) has the manifests and the
rules.

## Restore

To restore a volume, delete the claim, create a `PersistentVolume` with
the same URL, and bind a new claim to it. The pod starts on any node
from the last push, and the driver restores the file modes and empty
directories from `refs/git-csi/metadata`.

## Work trees the node keeps

The work tree stays on the node after the pod stops, so the next stage
on the same node does not clone the repository again. When you delete
the volume, the `persistentVolumeReclaimPolicy` of the `PersistentVolume`
decides what happens to the tree on each node that staged it. A
`PersistentVolume` that you create has `Retain` unless you set another
policy.

- **`Retain`** keeps each tree. A new `PersistentVolume` with the same
  `volumeHandle` stages from the tree on the same node, and its next
  push sends any commit that the last unstage could not push.
  [Restore](#restore) and [the credential](#the-credential) depend on
  this. The driver never removes a tree under `Retain`.
- **`Delete`** removes each tree, including any commit in it that no
  push sent. When you delete the claim, the `external-provisioner`
  deletes the released `PersistentVolume`, and the node plugin on each
  node removes its own tree. You can also delete the `PersistentVolume`
  first. A node plugin that is down at that time removes its tree when
  it starts again.

The driver never deletes the remote repository, under either policy.
The pod's unstage pushes before the volume leaves the node, so `Delete`
loses only a commit whose push failed. The node plugin's log names each
tree that it removes, and says whether the tree held an unpushed commit
or a side branch.

So that the node's store does not grow with every ref a volume once
used, the driver cleans it once an hour and after each tree that it
removes. It deletes the refs under `refs/git-csi/` that no volume uses,
removes each bare repository that no volume and no work tree uses, and
runs `git gc` in each bare repository that remains. When the driver
cannot read the volumes in its store, it cannot tell which repository a
work tree uses, so it deletes nothing, logs a warning, and tries again
at the next cleaning.

## What the driver does not serve

A volume is a checkout of one ref of one repository. The directory of a
submodule is empty, and a Git LFS file appears as its pointer file, not
as the content that the pointer names. A writeable volume does not
accept `depth`.

## What the driver reports

The events on the pod and on the claim use these reasons:
`GitVolumeArmed`, `GitVolumeUnarmed`, `GitVolumePending`,
`GitVolumePushed`, `GitVolumePushFailed`, `GitVolumeFileSkipped`,
`GitVolumeRebased`, `GitVolumeDiverged`, `GitVolumeHealed`,
`GitVolumeNoPublishSecret`, `GitVolumeUpstreamMoved`,
`GitVolumeRefDeleted`, and `GitVolumeRefused`. The node plugin's `/metrics` listener exports
`git_csi_volume_abnormal`, which is 1 while anything is wrong with a
volume, labeled with the namespace and the volume. It also exports
`git_csi_armed`, `git_csi_pending_paths`, `git_csi_unpushed_commits`,
`git_csi_last_push_timestamp_seconds`, `git_csi_push_failures_total`,
`git_csi_skipped_files`, and `git_csi_diverged`, each labeled with the
namespace and the claim.
