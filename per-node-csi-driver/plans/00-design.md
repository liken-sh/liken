# Design

`per-node-csi-driver` gives a pod a directory on the node it runs on.
The directory stays on that node when the pod leaves, and the same pod
finds it again if it comes back to that node. A pod that lands on a
different node finds an empty directory there. One volume has one
directory on every node it has ever visited, so the volume is a set of
copies, one per node, and the driver keeps none of them in agreement.

The driver name is `per-node.liken.sh`. The StorageClass is `per-node`.
It runs on a `liken` cluster, and on any other cluster whose CSI
plumbing is standard.

## Why this exists

Kubernetes has two kinds of node storage, and neither fits a workload
that replicates its own data. An ephemeral volume vanishes with the
pod. A local volume (`local`, or `local-path`) pins the pod to the node
that holds the data, so a dead node leaves the pod stuck until a person
or an operator deletes both the pod and its claim.

A workload that keeps its own copies in agreement wants neither. It
wants to land anywhere, keep its copy if it comes back, and rebuild
from its peers if it does not. That is the shape of a Corrosion
cluster, of most gossip-replicated stores, and of any cache.

## The two uses

**A replicated store.** Several pods each hold a full copy of one data
set and keep the copies in agreement over the network. Corrosion is
the first-class case: every pod runs a Corrosion agent beside its
program, the agents gossip changes, and a fresh copy catches up from
the others. A Deployment with one `per-node` claim, required
anti-affinity across nodes, and a PodDisruptionBudget is the whole
workload. It needs no StatefulSet, because no copy is first and the
copies start in any order.

**A named cache.** One or more pods fill a directory with items they
can make again, such as decoded art or fetched files. A copy that
lacks an item fills it. A pod that lands on a new node starts with an
empty cache and warms it.

A workload that is neither has no use for this class. A plain file
written on one node is not on the next, and nothing here will carry it
over.

## What the driver is not

- It is not a provisioner. A PersistentVolume of this class is written
  up front by whoever wants the volume, a person or an operator, and a
  claim binds to it. The driver has no controller.
- It is not a replicator. The copies on two nodes agree only if the
  workload makes them agree.
- It is not a budget. It keeps no size limit and reports its bytes for
  a person to read. Every copy shares the node's pod-storage partition
  with every other volume there.

## The invariants

1. **One volume, one directory per node.** A volume's handle names its
   directory under the store on every node. The handle is the
   PersistentVolume's `spec.csi.volumeHandle`, and by convention it is
   the PersistentVolume's name.
2. **A copy outlives its pod.** Unpublish unmounts and removes nothing.
3. **A copy dies with its volume.** The node plugin watches the
   PersistentVolumes of this driver. A directory whose handle no
   PersistentVolume names is removed, once the watch has synced and
   only while no pod holds it.
4. **One pod per node per volume.** A second pod on the same node that
   asks for a handle another pod holds is refused with an event on the
   pod. The scheduler's anti-affinity prevents this; the driver
   enforces it.
5. **The access mode is `ReadWriteMany`.** Many nodes do mount one
   volume read-write. Every page of the manual that shows a claim also
   says that each node holds its own copy. `ReadWriteOnce` would lie about the nodes,
   and `ReadWriteOncePod` would break the Deployment.
6. **No attach, no stage, no capacity.** `attachRequired: false`, no
   `STAGE_UNSTAGE_VOLUME` capability, `storageCapacity: false`. A
   publish is a `mkdir` and a bind mount.

## The volume's shape

A person or an operator writes two objects. The PersistentVolume names
the driver, the handle, the class, `ReadWriteMany`, a capacity that is
a hint and not a limit, `Retain`, and a `claimRef` so it binds to one
claim and no other. The claim names the class, `ReadWriteMany`, and
the same size.

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: media-catalog
spec:
  storageClassName: per-node
  accessModes: [ReadWriteMany]
  capacity:
    storage: 20Gi
  persistentVolumeReclaimPolicy: Retain
  claimRef:
    namespace: media
    name: catalog
  csi:
    driver: per-node.liken.sh
    volumeHandle: media-catalog
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: catalog
  namespace: media
spec:
  storageClassName: per-node
  accessModes: [ReadWriteMany]
  resources:
    requests:
      storage: 20Gi
```

Delete the PersistentVolume to remove every copy. Deleting the claim
alone leaves the volume `Released` and every copy in place, because no
controller runs a deleter.

A handle is a DNS-1123 subdomain name: lower-case letters, digits,
`-` and `.`, at most 253 characters, and no `/`. The driver refuses any
other handle, because the handle is a path under the store.

## The store

The store is `/var/lib/liken/pod-storage/per-node` on every node, a
hostPath mount into the node plugin. On `liken` that is the
pod-storage partition, so a copy is a tenant of the same disk as every
`local-path` volume and every `git-csi` store. `local-path` names its
directories `pvc-<uid>_<namespace>_<claim>` at the root and touches
only those, so the subdirectory is invisible to it.

Inside the store, `copies/<handle>/` is the copy. The driver creates it with
mode `0777` on the first publish on that node, the way `local-path`
does, so a pod that runs as any user can write it. `fsGroupPolicy` is
`None`, because a recursive chown of a large database on every start
is the wrong cost.

The plugin keeps its records of held handles beside the copies, under
`holds/<handle>`, so a restart of the plugin
recovers which pod holds which copy from the kubelet's target paths
and never from memory alone.

## What the driver reports

- `NodeGetVolumeStats` answers the copy's bytes and the store's free
  bytes, so `kubelet_volume_stats_*` carries them.
- A Prometheus gauge, `per_node_copy_bytes{volume}`, per copy on this
  node, on a port named `metrics`.
- An event on the pod for every refusal: a bad handle, a held handle,
  a failed mount.

## Deployment

`deploy/` is a kustomize base: the CSIDriver, the StorageClass, RBAC,
and a DaemonSet in `liken-system` with the node plugin and the
upstream `csi-node-driver-registrar`. The plugin is privileged for the
bind mount and mounts the kubelet's pod directory with bidirectional
propagation, the same as `git-csi-driver`'s node plugin. The DaemonSet
tolerates every taint, because a `liken` node registers with taints and
a store must exist on every node a pod can reach.

RBAC: the node plugin lists and watches PersistentVolumes, and creates
and patches Events. Nothing else.

## Releases

The same as every `liken` repository. A pushed calendar tag
(`2026.09.07-001`) is a release and moves `:latest`. A push to main is
a development build named `<release>-dev-<count>-<sha>` that never
moves `:latest`. `release.yaml` waits for `ci.yaml` to pass before it
pushes an image. The manual publishes to `per-node.liken.sh` from the
same run.

## Limits, stated

- No size budget. The best-effort check is a later plan.
- No caught-up signal. Whether a fresh copy has caught up is the
  workload's fact, and its readiness probe is where that belongs.
- No provisioner. A claim alone stays `Pending`. The dynamic path is a
  later plan if a consumer that is not an operator needs it.
