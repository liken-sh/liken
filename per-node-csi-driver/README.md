# per-node-csi-driver

`per-node-csi-driver` gives a pod a directory on the node it runs on.
The directory stays on that node after the pod leaves, and a pod that
comes back to that node finds it again. A pod that starts on a
different node gets an empty directory there. So one volume is a set
of copies, one on each node, and the pod can start on any node. A
`local` volume, by contrast, ties the pod to the node that has its
data, so the pod cannot start while that node is down.

The driver creates a copy the first time a pod mounts the volume on
a node. It keeps the copy until you delete the `PersistentVolume`. It
never copies data between nodes, and it does nothing to make the
copies match. So it suits only a workload that can fill an empty copy
by itself. A replicated store can: it keeps a full copy in each pod,
and a new copy catches up from the others over the network. The
Corrosion agents of a `library-operator` catalog work this way. A
cache can too: it fills its copy with items that it can make again,
such as decoded art or fetched files. Do not use the driver for any
other workload, because a file written on one node is not there on
the next. The driver sets no size limit. Every copy shares the node's
disk with the other volumes there, and the driver reports the size of
each copy in bytes. It does not report when a new copy has caught up,
because only the workload can tell that, through its readiness probe.

The driver runs on a `liken` cluster, where it stores the copies on
the pod-storage partition. The design also fits another cluster whose
kubelet uses the standard paths under `/var/lib/kubelet`. On such a
cluster, change the store path `/var/lib/liken/pod-storage/per-node`
in `deploy/node.yaml`. When a `library-operator` `Catalog` uses a storage
class of this driver, `library-operator` creates the volumes and runs
more than one copy of the catalog.

## The objects

The driver defines no custom resources. It has no controller, so it
creates no volumes. The deploy base installs the `StorageClass`
`per-node`.

- A `PersistentVolume` sets the driver `per-node.liken.sh`, the class
  `per-node`, and a `volumeHandle`. The handle is the name of the
  copy's directory on every node. Delete the `PersistentVolume` to
  remove every copy.
- A `PersistentVolumeClaim` binds to that volume. A claim with no
  volume stays `Pending`.

## A volume and its claim

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: example-cache
spec:
  storageClassName: per-node
  accessModes: [ReadWriteMany]
  capacity:
    storage: 5Gi
  persistentVolumeReclaimPolicy: Retain
  claimRef:
    namespace: example
    name: cache
  csi:
    driver: per-node.liken.sh
    volumeHandle: example-cache
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: cache
  namespace: example
spec:
  storageClassName: per-node
  accessModes: [ReadWriteMany]
  resources:
    requests:
      storage: 5Gi
```

The manual is at [liken.sh/per-node](https://liken.sh/per-node/), and
[`plans/00-design.md`](plans/00-design.md) is the design.

MIT. See [LICENSE](LICENSE).
