---
title: Mount a repository read-only
weight: 20
---

A read-only volume is an inline CSI volume in the pod spec. It needs no
`PersistentVolume` and no claim, so any pod in any namespace can mount
any repository the node can reach.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: reader
spec:
  containers:
    - name: reader
      image: debian:12-slim
      command: ["sleep", "infinity"]
      volumeMounts:
        - name: data
          mountPath: /data
          readOnly: true
  volumes:
    - name: data
      csi:
        driver: git.liken.sh
        readOnly: true
        volumeAttributes:
          url: https://example.com/data/franchises.git
          ref: main
          pull: 5m
```

The pod sees a plain directory with the files of the ref and no `.git`.
Every volume of the same URL on a node shares one bare repository, so
ten pods on one repository cost one fetch. The driver fetches every
`pull`, and when the ref moved it replaces the files under the mount one
by one, so a reader sees the old file or the new one and never a partial
write.

The [attributes reference](../../reference/attributes/) lists every
attribute, its values, and its default.

## A claim on a repository

A workload that names its storage as a `PersistentVolumeClaim` cannot
mount an inline volume. For that workload, the driver serves a
repository as a static `PersistentVolume` with the access mode
`ReadOnlyMany`, and a claim binds it. The attributes are the ones the
inline form takes.

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

A pod mounts the claim with `readOnly: true` on its volume. The
container runtime binds a volume into a pod read-write unless the pod
asks for read-only, whatever the driver's own mount says, so the driver
refuses a pod that does not ask, with `GitVolumeRefused` in the pod's
events.

```yaml
volumes:
  - name: franchises
    persistentVolumeClaim:
      claimName: franchises
      readOnly: true
```

Many pods on one node publish one tree, each at its own mount, and the
driver keeps the tree until the last of them stops. A private repository
names its `Secret` through `nodeStageSecretRef`, with the keys the inline
form takes through `nodePublishSecretRef`. The driver ignores a
`VolumeAttributesClass` on such a claim, because a read-only volume
commits nothing and pushes nothing.

## When the remote is unreachable

By default a volume whose fetch fails at start is refused, and the pod
stays in `ContainerCreating` with the reason in its events. Set
`offline: allowStale` to publish the node's last copy of the ref
instead. The abnormal gauge and the log then report the fetch error
until a fetch succeeds. A repository the node has never fetched is refused
under both settings, because there is nothing to publish.

## Private repositories

Name a `Secret` in the pod's namespace with `nodePublishSecretRef`. The
driver reads two kinds of credential from it:

- `ssh-privatekey`, for an SSH URL, with an optional `known_hosts`. With
  `known_hosts`, the driver checks the host key. Without it, the driver
  accepts the first key it sees and refuses a later change.
- `token`, for an HTTPS URL, with an optional `username`. The default
  username is `git`.

```yaml
    - name: data
      csi:
        driver: git.liken.sh
        readOnly: true
        volumeAttributes:
          url: git@example.com:data/private.git
        nodePublishSecretRef:
          name: data-deploy-key
```

The credential reaches the node's disk only for the length of one git
invocation, and the token never appears on a command line.

## What the driver reports

A refused mount, a stale publish, and a fetch that fails after one that
worked each post an `Event` on the pod. `kubectl describe pod` shows
them.
A read-only claim posts each of those on every pod it is published to
and on the claim, so `kubectl describe pvc` shows them too. The node
plugin's gauge `git_csi_volume_abnormal`, labeled by the
pod's namespace and the volume, is one after a stale publish and after
a failed fetch, until the next fetch succeeds.
