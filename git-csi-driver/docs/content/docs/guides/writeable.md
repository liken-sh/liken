---
title: Give an application a repository to write
weight: 30
---

A writeable volume is a `PersistentVolume` that names a repository, a
`PersistentVolumeClaim` that binds it, and a `VolumeAttributesClass`
that says how the driver commits and pushes. The application sees a
plain directory and writes to it as it always did. The driver commits
what it wrote and pushes it.

## The volume

The `PersistentVolume` holds what identifies the volume: the
repository, the ref, and the credentials. Its `csi` block cannot
change after creation, so nothing that a person tunes lives here.

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
    nodePublishSecretRef:
      name: homeassistant-deploy-key
      namespace: home
```

`capacity` is required by the API and means nothing to the driver. The
access mode must be `ReadWriteOncePod`. `ReadWriteOnce` allows two pods
on one node to write the same tree, and the driver refuses it.

## The claim

The claim names the volume. Until it also names a class, the volume is
unarmed: the driver watches the tree and reports what it would commit,
and commits nothing. This is the moment to write the repository's
`.gitignore`, before the first commit can carry a token or a database.

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

The [class reference](../../reference/classes/) lists every parameter,
its values, and its default.

## What happens after a write

The driver waits until the tree has been quiet for `push.quiesce`,
then commits every changed path that is not ignored and not over
`commit.maxFileSize`. It pushes when the quiesce passes with no new
write, or when the oldest unpushed commit is older than
`push.maxLatency`, and always when the pod stops. Modes, owners, and
empty directories are recorded on a ref of the driver's own,
`refs/git-csi/metadata`, which never appears in the tree or on the
forge's file view.

## When upstream moves

The application's tree changes only when the application writes it.
Upstream reaches the tree at stage, when the pod starts, and never
later. At stage the driver compares the tree to the ref:

- **Behind.** The tree takes upstream.
- **Ahead.** Nothing changes. The next push carries the commits.
- **Diverged.** The driver rebases the tree's commits onto upstream. A
  rebase that conflicts is aborted, and the volume moves to a side
  branch.
- **Uncommitted writes.** A tree with writes no commit carries yet is
  left as it is, whatever upstream did, and the condition says upstream
  moved.

A push the forge rejects, or an aborted rebase, moves the volume to the
branch `<ref>.<volumeHandle>`. Every push goes there until a person
merges it into the ref on the forge. The condition says `Diverged` with
both branch names, and commits continue, so no work stops. At the next
pod start after the merge, the volume is back on the ref and the side
branch is deleted.

## Restore

Delete the claim, make a `PersistentVolume` against the same URL, and
bind a new claim to it. The pod starts on any node from the last push,
with its modes and empty directories replayed.

## Work trees the node keeps

A work tree stays on the node after the pod stops, so the next stage on
the same node is not a clone. Once an hour the driver removes work trees
that nothing has staged for `--sweep-after`, 30 days by default, and
whose every commit the remote holds. A tree with unpushed commits is
never removed. Its age is named in the condition of the next volume of
the same repository, so a person learns that work sits on the node with
no claim that reaches it.

## What the driver reports

The pod's events and the claim's events carry `GitVolumeArmed`,
`GitVolumeUnarmed`, `GitVolumePending`, `GitVolumePushed`,
`GitVolumePushFailed`, `GitVolumeFileSkipped`, `GitVolumeDiverged`,
`GitVolumeHealed`, and `GitVolumeSwept`. The volume's condition is abnormal for an unarmed volume
with pending paths, a failed push, a skipped file, and an unpushed
commit older than `push.maxLatency`. The node plugin's `/metrics`
listener exports `git_csi_armed`, `git_csi_pending_paths`,
`git_csi_unpushed_commits`, `git_csi_last_push_timestamp_seconds`,
`git_csi_push_failures_total`, `git_csi_skipped_files`, and
`git_csi_diverged`, labeled by namespace and claim.
