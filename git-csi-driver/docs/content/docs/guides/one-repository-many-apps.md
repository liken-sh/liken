---
title: Give many applications one repository
weight: 40
description: "Give many applications one git repository, one directory each, as separate writeable volumes on the same ref. Use when several applications share one configuration repository and none may read another's files."
---

# Give many applications one repository

One repository can hold the configuration of many applications, one
directory per application. Each application gets its own writeable
volume on the same repository and the same ref. The volume's `paths`
names the application's directory, so the node checks out only that
directory for it, and the driver commits only changes inside it.
`subPath` then makes the directory the root of the container's mount.
No application reads or writes another one's files.

Every volume here is a writeable volume, which [Give an application a
repository to write](../writeable/) describes. The example repository is
`git@code.example.com:home/configuration.git`. It holds
`assistant/configuration/` for a home automation service and `maps/site/`
for a static site.

## A volume for each application

Each application gets its own `PersistentVolume`, with the same URL
and ref, and with its own directory in `paths`. Each needs its own
`volumeHandle`, because the driver names the volume on the node by its
handle.

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: assistant-configuration
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadWriteOncePod]
  persistentVolumeReclaimPolicy: Retain
  volumeAttributesClassName: assistant-configuration
  csi:
    driver: git.liken.sh
    volumeHandle: assistant-configuration
    volumeAttributes:
      url: git@code.example.com:home/configuration.git
      ref: main
      paths: assistant/configuration
    nodeStageSecretRef:
      name: assistant-deploy-key
      namespace: home
    nodePublishSecretRef:
      name: assistant-deploy-key
      namespace: home
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: maps-site
spec:
  capacity: {storage: 1Gi}
  accessModes: [ReadWriteOncePod]
  persistentVolumeReclaimPolicy: Retain
  volumeAttributesClassName: maps-site
  csi:
    driver: git.liken.sh
    volumeHandle: maps-site
    volumeAttributes:
      url: git@code.example.com:home/configuration.git
      ref: main
      paths: maps/site
    nodeStageSecretRef:
      name: maps-deploy-key
      namespace: home
    nodePublishSecretRef:
      name: maps-deploy-key
      namespace: home
```

Give each application entries that no other application's entries
contain. When the entries of two volumes overlap, both applications
can change the same file, and [when both applications write one
file](#when-both-applications-write-one-file) describes what happens
then.

Here each volume has its own deploy key, named in both secret
references. Because both volumes push to the same repository, they can
also share one `Secret`. [The credential](../writeable/#the-credential) explains why a
volume references its `Secret` twice.

## A claim for each application

Each claim binds one volume and sets a class. Kubernetes binds a claim
to a pre-created volume only when both have the same
`volumeAttributesClassName`, so each `PersistentVolume` above sets the
same class as its claim.

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: assistant-configuration
  namespace: home
spec:
  volumeName: assistant-configuration
  accessModes: [ReadWriteOncePod]
  resources: {requests: {storage: 1Gi}}
  volumeAttributesClassName: assistant-configuration
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: maps-site
  namespace: home
spec:
  volumeName: maps-site
  accessModes: [ReadWriteOncePod]
  resources: {requests: {storage: 1Gi}}
  volumeAttributesClassName: maps-site
```

## A class for each application

The classes can differ. The home automation service writes a database
beside its configuration, so its class ignores those files. The static
site changes in bursts, so its class waits longer for the tree to go
quiet.

```yaml
apiVersion: storage.k8s.io/v1
kind: VolumeAttributesClass
metadata:
  name: assistant-configuration
driverName: git.liken.sh
parameters:
  push.quiesce: 30s
  commit.author: Assistant <assistant@home.example>
  ignore: "*.db*,*.log"
---
apiVersion: storage.k8s.io/v1
kind: VolumeAttributesClass
metadata:
  name: maps-site
driverName: git.liken.sh
parameters:
  push.quiesce: 5m
  commit.author: Maps <maps@home.example>
```

The [class reference](../../reference/classes/) lists every parameter,
its values, and its default.

## One directory in each pod

Set `subPath` on the volume mount to the same directory as `paths`, so
the container's mount path is the application's directory. `paths`
keeps each entry at its place in the repository, so without `subPath`
the container finds its files under `/config/assistant/configuration`.
Without `paths`, `subPath` still hides the other directories from the
container, but the node checks out every application's files in each
volume's tree. Use `strategy: Recreate` on the
`Deployment`. A rolling update starts the new pod before it stops the
old one, and `ReadWriteOncePod` never lets the second pod start, so
the rollout waits forever.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: assistant
  namespace: home
spec:
  replicas: 1
  strategy: {type: Recreate}
  selector:
    matchLabels: {app: assistant}
  template:
    metadata:
      labels: {app: assistant}
    spec:
      containers:
        - name: assistant
          image: registry.example.com/assistant:1
          volumeMounts:
            - name: configuration
              mountPath: /config
              subPath: assistant/configuration
      volumes:
        - name: configuration
          persistentVolumeClaim: {claimName: assistant-configuration}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: maps
  namespace: home
spec:
  replicas: 1
  strategy: {type: Recreate}
  selector:
    matchLabels: {app: maps}
  template:
    metadata:
      labels: {app: maps}
    spec:
      containers:
        - name: maps
          image: registry.example.com/maps:1
          volumeMounts:
            - name: site
              mountPath: /site
              subPath: maps/site
      volumes:
        - name: site
          persistentVolumeClaim: {claimName: maps-site}
```

## When both applications push

Each volume pushes on its own schedule. When both push close
together, the forge rejects the second push, because the ref has
moved. The driver then fetches the ref, rebases the volume's commits
onto it, and pushes again, up to three times. The other application's
commits changed only files outside this volume's `paths`, so the
driver writes no file into the pod's tree. The claim gets a
`GitVolumeRebased` event.

## When both applications write one file

When the application and upstream both changed the same file, the
rebase conflicts. The driver moves that volume to the side branch
`<ref>.<volumeHandle>`, posts `GitVolumeDiverged`, and keeps
committing and pushing there, so the application keeps working. When
a person merges the side branch into the ref on the forge, the volume
returns to the ref at its next push, and the driver deletes the side
branch. [When upstream
moves](../writeable/#when-upstream-moves) in the writeable guide gives
the full rule.

## The metadata record

The driver records file modes, owners, and empty directories on the
ref `refs/git-csi/metadata`, as the writeable guide describes. All the
volumes of one repository write the same ref, so the record is merged
by path. A volume with `paths` records the files inside its entries
only. When it writes the record, it keeps each line for a path outside
its entries as it last read that line from the forge, at a stage or a
rebase, and writes its own lines for the paths inside them. So the
two applications keep each other's modes and empty directories. When
the forge rejects a push of the record, the driver merges its lines
with the forge's record in the same way, and commits the result with
the forge's record as its parent, so the next push of the record is a
fast-forward. At a stage or a restore, a volume applies only the lines
inside its entries. Leave the `metadata` parameter at `true`.

## A new application

To add an application, give it its own directory in `paths`, even
when the repository has no such directory yet. A writeable volume
stages when its commit lacks an entry, and checks out nothing for it.
The kubelet creates the `subPath` directory when the pod starts, and
the driver commits the first files that the application writes there.
The new application's commits change no file inside the other
volumes' entries, so the trees of the other applications do not
change.

## A read-only claim beside the writers

A read-only claim on the same repository works as usual beside the
writeable ones. The driver ignores a `VolumeAttributesClass` on a
read-only claim, because a read-only volume never commits or pushes. [Mount a repository
read-only](../read-only/) gives its form.
