---
title: Give many applications one repository
weight: 40
---

One repository can hold the configuration of many applications, one
directory per application. Each application gets its own writeable
volume on the same repository and the same ref, and mounts only its own
directory. No application reads or writes another one's files.

Every volume here is a writeable volume, which [Give an application a
repository to write](../writeable/) describes. The example repository is
`git@code.example.com:home/configuration.git`. It holds
`assistant/configuration/` for a home automation service and `maps/site/`
for a static site.

## A volume for each application

Each application gets its own `PersistentVolume`. The URL and the ref
are the same on both. The `volumeHandle` is not: it names the volume on
the node, so each volume needs its own.

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
    nodePublishSecretRef:
      name: maps-deploy-key
      namespace: home
```

Each volume names its own deploy key here. Two volumes can name one
`Secret` instead, because they push to one repository.

## A claim for each application

Each claim binds one volume and names a class. The binder pairs a claim
and a static volume only when both name the same class, so the
`PersistentVolume` above carries the same `volumeAttributesClassName`.

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
site is edited in bursts, so its class waits longer for the tree to go
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

`subPath` on the volume mount publishes one directory of the tree into
the container. The container sees that directory and nothing else of the
repository. Use `strategy: Recreate` on a `Deployment`, because a
rolling update would wait forever for a second pod that
`ReadWriteOncePod` never lets start.

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

Each volume commits and pushes on its own. The first push of the two
lands. The second finds the ref moved, and the forge rejects it. The
driver then fetches, rebases the volume's commits onto upstream beside
the pod's tree, and pushes again, three times at most. The pod's tree
takes the result in one step that rewrites only the files upstream
changed. Those files are the other application's, and `subPath` keeps
them out of its pod. The claim's events carry `GitVolumeRebased`, with
the count of commits and the upstream commit.

## When both applications write one file

The rebase does not settle a file that the application and upstream both
changed. That volume moves to the branch `<ref>.<volumeHandle>`, and its
events carry `GitVolumeDiverged`. Every push goes there until a person
merges it into the ref on the forge, and commits continue, so no work
stops. At the volume's next push after the merge, it is back on the
ref and the side branch is deleted. The writeable guide's [When upstream
moves](../writeable/#when-upstream-moves) gives the full rule.

## The metadata record

The modes, owners, and empty directories of the tree are recorded on
one ref, `refs/git-csi/metadata`, as the writeable guide says. Two
writers share it. After a rebase, a volume takes the other
application's modes for the files the rebase brought in, and a
record the forge rejects is rebuilt on the record the forge holds, so
neither application's record overwrites the other's. `metadata` stays
on.

## A read-only claim beside the writers

A read-only claim on the same repository takes no part in any of this.
The driver ignores a `VolumeAttributesClass` on such a claim, because a
read-only volume commits nothing and pushes nothing. [Mount a repository
read-only](../read-only/) gives its form.
