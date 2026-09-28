---
name: writeable
description: "Give an application a git repository as a writeable volume that the driver commits and pushes. Use when an application writes files that must go into git, when upstream moves, when several writers share one repository, or to restore a volume."
---

This skill is the guide at https://git.liken.sh/docs/guides/writeable/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

A writeable volume is a `PersistentVolume` that names a repository, a
`PersistentVolumeClaim` that binds it, and a `VolumeAttributesClass`
that says how the driver commits and pushes. The application gets a
plain directory and writes to it as it always does. The driver commits
what the application wrote and pushes it.

## The volume

The `PersistentVolume` holds what identifies the volume: the
repository, the ref, and the credentials. Its `csi` block cannot
change after creation, so nothing that a person tunes is here.

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

`capacity` is required by the API, and the driver ignores it. The
access mode must be `ReadWriteOncePod`. `ReadWriteOnce` allows two pods
on one node to write the same tree, and the driver refuses it.

A private repository names one `Secret` in both references.
[The credential](#the-credential) says why.

## The claim

The claim names the volume. Until it also names a class, the volume is
unarmed: the driver watches the tree and reports what it would commit,
and commits nothing. Write the repository's `.gitignore` now, before
the first commit can include a token or a database.

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

Mount the claim in the application's pod as any other claim. Use
`strategy: Recreate` on a `Deployment`, because a rolling update would
wait forever for a second pod that `ReadWriteOncePod` never lets
start.

## The class

A `VolumeAttributesClass` is the cluster owner's word for a commit and
push policy. Set it on the claim to arm the volume. The field is
mutable, so a policy change never restarts the application.

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

Set the class after the claim is bound. The binder pairs a claim and a
static volume only when both name the same class. So a claim that names
a class before it binds needs the same `volumeAttributesClassName` on
the `PersistentVolume`. A bound claim takes a class change without that.

The [class reference](https://git.liken.sh/docs/reference/classes/) lists every parameter,
its values, and its default.

## The credential

The kubelet sends a volume's `Secret` inside its calls to the driver,
and the driver keeps it in memory, never on the node's disk. The stage
call carries `nodeStageSecretRef`, and the driver fetches the ref with
it before the pod starts. Each publish call carries
`nodePublishSecretRef`. The kubelet calls publish again for every
mounted volume on each pod sync, about once a minute, and reads the
`Secret` again for each call.

So a driver that restarts, for example in an upgrade, holds no
credential until the next publish. Then it pushes what the tree
committed at once, with no restart of the application. Until then, the
volume's report and the abnormal gauge say the volume waits for its
credential.

A rotated `Secret` reaches the driver at the next publish, and the
driver then pushes every commit the remote does not hold yet, at once,
with the new key. So a commit that a push with a revoked key could not
send does not wait for the next timed push. A publish whose `Secret`
differs from the stage's is refused with `GitVolumeRefused`, because the driver cannot choose
between two credentials for one volume.

A `PersistentVolume` with `nodeStageSecretRef` and no
`nodePublishSecretRef` works until the driver restarts, and then the
driver pushes nothing for it until the pod is deleted and started
again. The driver posts
`GitVolumeNoPublishSecret` on the pod and the claim when it publishes
such a volume. The `csi` block cannot change, so give the volume a new
`PersistentVolume`. Do it before an upgrade of the driver, because the
upgrade restarts the driver. After a restart, the driver does not
push at step 1 either, and posts `GitVolumePushFailed`. The commits
stay in the node's work tree until the volume is
staged on the same node again.

1. Scale the application to zero. The driver pushes what the tree holds
   when the pod stops.
2. Delete the claim and then the `PersistentVolume`. With
   `persistentVolumeReclaimPolicy: Retain`, the repository and the
   node's work tree stay.
3. Create the `PersistentVolume` again with the same `volumeHandle` and
   both references, and create the claim again.
4. Scale the application up. On the same node, the stage starts from
   the work tree the node kept.

## What happens after a write

The driver waits until the tree has been quiet for `push.quiesce`,
then commits every changed path that is not ignored and not over
`commit.maxFileSize`. It pushes when the quiesce passes with no new
write, or when the oldest unpushed commit is older than
`push.maxLatency`, and always when the pod stops. A write made while
the driver was not running, for example during an upgrade, sends the
driver no event. So the driver also starts the quiesce when it starts
to watch the tree, and commits that write when the quiesce passes.

The driver records modes, owners, and empty directories on a ref of its
own, `refs/git-csi/metadata`. That ref never appears in the tree or on the
forge's file view.

## When upstream moves

The application's tree changes only when the application writes it,
with one exception below. The driver applies upstream to the tree at
stage, when the pod starts. At stage the driver compares the tree to
the ref:

- **Behind.** The tree takes upstream.
- **Ahead.** Nothing changes. The next push includes the commits.
- **Diverged.** The driver rebases the tree's commits onto upstream.
  The driver aborts a rebase that conflicts, and the volume moves to a
  side branch.
- **Uncommitted writes.** When the tree has writes that no commit
  holds yet, the driver leaves the tree as it is, whatever upstream
  did. The abnormal gauge and the log then say upstream moved.

The exception is a push the forge rejects because the ref moved. The
driver then fetches, rebases the tree's commits onto upstream beside
the pod's tree, and pushes again, three times at most. The pod's tree
takes the result in one step that rewrites only the files upstream
changed. A file the application wrote since the last commit is kept,
unless upstream changed that same file. The claim's events include
`GitVolumeRebased` when this happens.

Three things move the volume to the branch `<ref>.<volumeHandle>`: a
push still rejected after the third rebase, an aborted rebase, or a
file the application and upstream both changed. Every push goes there
until a person merges it into the ref on the forge. The events and the
log name both branches, and commits continue, so no work stops. At the
volume's next push after the merge, or its next pod start, the volume
is back on the ref and the driver deletes the side branch.

## Many writers on one repository

One repository can hold the configuration of many applications, each
with its own writeable volume and its own directory mounted with
`subPath`. [Give many applications one
repository](https://git.liken.sh/docs/guides/one-repository-many-apps/) gives the manifests and the
rules.

## Restore

Delete the claim, make a `PersistentVolume` against the same URL, and
bind a new claim to it. The pod starts on any node from the last push,
with its modes and empty directories replayed.

## Work trees the node keeps

A work tree stays on the node after the pod stops, so the next stage on
the same node is not a clone. Once an hour the driver removes work trees
that nothing has staged for `--sweep-after`, 30 days by default, and
whose every commit the remote holds. A tree with unpushed commits is
never removed. Its age is named in the log and the abnormal gauge of
the next volume of the same repository, so a person learns that work
stays on the node with no claim that reaches it.

The same hourly pass deletes the refs under `refs/git-csi/` that no
volume follows, and runs `git gc` in each bare repository that stays.
The node's store then does not grow with every ref a volume ever
followed.

## What the driver does not serve

A checkout is one ref of one repository. A submodule's directory is
empty, a Git LFS pointer file is checked out as the pointer and not the
object it names, and a writeable volume takes no `depth`.

## What the driver reports

The pod's events and the claim's events include `GitVolumeArmed`,
`GitVolumeUnarmed`, `GitVolumePending`, `GitVolumePushed`,
`GitVolumePushFailed`, `GitVolumeFileSkipped`, `GitVolumeRebased`,
`GitVolumeDiverged`, `GitVolumeHealed`, `GitVolumeSwept`, and
`GitVolumeNoPublishSecret`. The node plugin's `/metrics`
listener exports `git_csi_volume_abnormal`, one while anything is wrong
with a volume, and `git_csi_armed`, `git_csi_pending_paths`,
`git_csi_unpushed_commits`, `git_csi_last_push_timestamp_seconds`,
`git_csi_push_failures_total`, `git_csi_skipped_files`, and
`git_csi_diverged`, labeled by namespace and claim.
