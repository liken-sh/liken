---
title: Volumes
weight: 10
---

# Volumes

You write two objects for each volume. The `PersistentVolume` sets
the driver, the handle, the class, an access mode, a capacity, the
reclaim policy `Retain`, and a `claimRef`. The driver does not enforce
the capacity. The `claimRef` binds the volume to one claim and no
other. The claim sets the same class, access mode, and size. In this
example the access mode is `ReadWriteMany`, so pods on many nodes can
mount the volume.

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

The handle is `spec.csi.volumeHandle`. Each node uses the handle as
the name of its copy's directory, so make the handle the same as the
`PersistentVolume`'s name, and the name in `kubectl get pv` is also
the directory's name. The handle must be a DNS-1123 subdomain name of
at most 253 characters: segments of lower-case letters, digits, and
`-`, separated by `.`, where each segment starts and ends with a
letter or a digit. The driver refuses to mount any other handle,
returns `InvalidArgument` to the kubelet, and posts a
`PerNodeVolumeRefused` `Event` on the pod.

## The access mode

Use `ReadWriteMany` for a volume that pods on many nodes mount. Each
node keeps its own copy. Every pod on one node that mounts the volume
gets that node's copy, so two pods on one node read and write the same
files. When each pod of a workload must have its own copy, keep the
pods on different nodes with required pod anti-affinity, as a
[replicated store](../../guides/replicated-store/) does. The scheduler
counts a terminating pod against the anti-affinity until the pod is
gone, so a replacement pod does not mount a copy while the old pod is
still running on it. A topology spread constraint is not enough,
because the scheduler does not count a terminating pod against it.

Use `ReadWriteOncePod` for a volume that only one pod in the whole
cluster may mount, such as a database with one writer. Set it on the
`PersistentVolume` and on the claim. The scheduler places only one pod
that uses a `ReadWriteOncePod` claim, and keeps every other pod that
uses the claim `Pending` until that pod is gone. A pod that starts on a
different node gets that node's copy, not the copy on the node where
the last pod ran. A `Deployment` with the `RollingUpdate` strategy
cannot start a new pod while the old pod still uses the claim, so give
it the `Recreate` strategy.

Do not use `ReadWriteOnce`. It tells Kubernetes that the volume is on
one node, and that is false for this driver, because every node keeps
its own copy.

The driver mounts the volume the same way for every access mode: it
bind-mounts this node's copy at the target path that the kubelet
gives it, read-only when
the pod mounts the volume read-only.

## What deleting removes

Delete the `PersistentVolume` to remove every copy. The node plugin on
each node watches the `PersistentVolume`s of this driver, and removes a
copy when no `PersistentVolume` has the copy's handle. It checks when a
`PersistentVolume` is deleted, and again every `--sweep-every`. It
does not check until it has listed every `PersistentVolume` once, so
it never removes a copy before it has read which volumes exist. It does
not remove a copy while a pod on that node mounts it. The copy stays
until that pod is gone, and the next check removes it.

Deleting the claim alone leaves the volume `Released` and every copy in
place, because no controller runs a deleter. Deleting a pod removes
nothing, because the copy outlives the pod.

## Events

The driver posts a `Warning` `Event` on the pod each time it refuses
or fails a mount, so `kubectl describe pod` shows why the mount did
not happen.
The driver does not refuse a pod because another pod on the node
mounts the same volume.

The kubelet retries a refused mount, and the wait between retries grows
to about two minutes. When the driver refuses a retry for the same
reason, with the same message, within 10 minutes of the last one, it
increases the count of the `Event` already posted and posts no new one.
So a pod whose mount fails for an hour shows one line, such as
`(x37 over 1h)`. The API server deletes an `Event` one hour after its
last change.

| Reason | When |
|---|---|
| `PerNodeVolumeRefused` | The handle is not a name that the driver can use for a directory under the store. The message gives the rule that the handle breaks. |
| `PerNodeMountFailed` | The driver could not create the copy, could not create the target, or could not bind the copy onto the target. The message includes the error. |
