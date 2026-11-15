---
title: Volumes
weight: 10
---

# Volumes

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
Every pod on one node that mounts the volume gets that node's copy, so
two pods on one node read and write the same files. When the pods of a
workload must each have a copy of their own, keep them on different
nodes with required pod anti-affinity, as a
[replicated store](../../guides/replicated-store/) does. The scheduler
counts a terminating pod against the anti-affinity until the pod is
gone, so a replacement pod never mounts a copy while the old pod still
runs on it. A topology spread constraint is not enough, because the
scheduler does not count a terminating pod against it.

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

The driver posts a Warning event on the pod for every refusal, so
`kubectl describe pod` says why a mount did not happen. The driver
never refuses a pod because another pod on the node mounts the same
volume.

The kubelet retries a refused mount with a backoff that grows to about
two minutes. Each retry that the driver refuses for the same reason,
with the same message, within 10 minutes of the last one, increases the
count of the event already posted and posts no new one. So a pod
whose mount fails for an hour carries one line, such as
`(x37 over 1h)`. The API server deletes an event one hour after
its last change.

| Reason | When |
|---|---|
| `PerNodeVolumeRefused` | The handle is not a name the driver can put under the store. The message says which rule it broke. |
| `PerNodeMountFailed` | The driver could not make the copy, could not make the target, or could not bind one onto the other. The message carries the error. |
