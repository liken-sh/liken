---
title: The volume
weight: 10
---

A person or an operator writes two objects. The `PersistentVolume`
names the driver, the handle, the class, an access mode, a capacity,
`Retain`, and a `claimRef`. The capacity is a hint, not a limit. The
driver enforces no size limit. The `claimRef` binds the volume to one
claim and no other. The claim names the class, the same access mode,
and the same size. In this example the access mode is `ReadWriteMany`:
many nodes mount this one volume read-write, and each node keeps its
own copy.

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: example-store
spec:
  storageClassName: per-node
  accessModes: [ReadWriteMany]
  capacity:
    storage: 20Gi
  persistentVolumeReclaimPolicy: Retain
  claimRef:
    namespace: example
    name: store
  csi:
    driver: per-node.liken.sh
    volumeHandle: example-store
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: store
  namespace: example
spec:
  storageClassName: per-node
  accessModes: [ReadWriteMany]
  resources:
    requests:
      storage: 20Gi
```

## The handle

The handle is `spec.csi.volumeHandle`. By convention it is the
`PersistentVolume`'s name, so the name a person reads in `kubectl get
pv` is the name of the directory on every node. The handle names the
volume's directory under the store on every node it has visited, so it
is a DNS-1123 subdomain name: lower-case letters, digits, `-` and `.`,
starting and ending with a letter or a digit, at most 253 characters,
no `/`, and no empty segment between two dots. The driver refuses any
other handle with `InvalidArgument` and posts a `PerNodeVolumeRefused`
event on the pod.

## The access mode

Use `ReadWriteMany` for a volume that pods on many nodes mount. Many
nodes do mount one volume read-write, and each node holds its own copy.
The driver enforces one pod per node per volume itself, and refuses a
second pod on the same node with a `PerNodeVolumeHeld` event.

Use `ReadWriteOncePod` for a volume that only one pod in the whole
cluster may mount, such as a database with one writer. Set it on the
`PersistentVolume` and on the claim. The scheduler admits one pod of a
`ReadWriteOncePod` claim in the cluster, and keeps every other pod of
the claim `Pending` until that pod is gone. A pod that starts on a
different node gets that node's copy, not the copy on the node the
last pod left. A `Deployment` with the `RollingUpdate` strategy cannot
start a new pod while its old pod holds the claim, so give it the
`Recreate` strategy.

Do not use `ReadWriteOnce`. It says that one node holds the volume,
which is false, because every node keeps its own copy.

The node plugin declares the CSI capability `SINGLE_NODE_MULTI_WRITER`,
so the kubelet sends the CSI access mode `SINGLE_NODE_SINGLE_WRITER`
for a `ReadWriteOncePod` claim. The driver publishes every access mode
the same way: it binds this node's copy onto the pod's target,
read-only when the pod mounts the volume read-only.

## What deleting removes

Delete the `PersistentVolume` to remove every copy. The node plugin on
each node watches the `PersistentVolume`s of this driver, and it
removes a copy whose handle no `PersistentVolume` names. It does so
after its watch has synced, only while no pod on that node holds the
copy, on the deletion itself and again on every `--sweep-every` tick.
A copy a pod still holds stays until that pod is gone.

Deleting the claim alone leaves the volume `Released` and every copy in
place, because no controller runs a deleter. Deleting a pod removes
nothing: the copy outlives its pod.

## Events

The driver posts an event on the pod for every refusal, so `kubectl
describe pod` says why a mount did not happen.

| Reason | When |
|---|---|
| `PerNodeVolumeRefused` | The handle is not a name the driver can put under the store. The message says which rule it broke. |
| `PerNodeVolumeHeld` | Another pod on this node holds the handle. The message names that pod. |
| `PerNodeMountFailed` | The driver could not make the copy, could not make the target, or could not bind one onto the other. The message carries the error. |
