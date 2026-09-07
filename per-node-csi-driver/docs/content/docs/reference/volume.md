---
title: The volume
weight: 10
---

A person or an operator writes two objects. The `PersistentVolume`
names the driver, the handle, the class, `ReadWriteMany`, a capacity,
`Retain`, and a `claimRef`. The capacity is a hint, not a limit: the
driver enforces no size. The `claimRef` binds the volume to one claim
and no other. The claim names the class, `ReadWriteMany`, and the same
size. Many nodes mount this one volume read-write, and each node holds
its own copy.

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

The access mode is `ReadWriteMany`, because many nodes do mount one
volume read-write, and each node holds its own copy. `ReadWriteOnce`
would say that one node holds the volume, which is false.
`ReadWriteOncePod` would let one pod in the cluster mount it, which
breaks the `Deployment`. The driver enforces one pod per node per
volume itself, and refuses a second pod on the same node with a
`PerNodeVolumeHeld` event.

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
