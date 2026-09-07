---
title: A replicated store
weight: 20
---

A replicated store is several pods that each hold a full copy of one
data set and keep the copies in agreement over the network. Each pod
runs the store beside its program, the copies exchange changes, and a
fresh copy catches up from the others. On this driver the whole
workload is a `Deployment` with one `per-node` claim, required
anti-affinity across nodes, and a `PodDisruptionBudget`. It needs no
`StatefulSet`, because no copy is first and the copies start in any
order.

Write the `PersistentVolume` and the claim up front, because the driver
provisions nothing. The handle is the volume's name. The capacity is a
hint, not a limit. The `claimRef` binds the volume to one claim and no
other. The access mode is `ReadWriteMany`, because many nodes mount
this one volume read-write, and each node holds its own copy.

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
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: store
  namespace: example
spec:
  replicas: 3
  selector:
    matchLabels:
      app: store
  template:
    metadata:
      labels:
        app: store
    spec:
      # One pod per node. The driver refuses a second pod that asks for
      # a handle another pod on the same node holds, and this
      # anti-affinity keeps the scheduler from placing one.
      affinity:
        podAntiAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            - topologyKey: kubernetes.io/hostname
              labelSelector:
                matchLabels:
                  app: store
      containers:
        - name: store
          image: ghcr.io/example/store:1
          volumeMounts:
            - name: data
              mountPath: /data
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: store
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: store
  namespace: example
spec:
  # A fresh copy catches up from its peers. A drain that took two pods
  # at once would leave one copy to serve alone and to seed both
  # replacements.
  maxUnavailable: 1
  selector:
    matchLabels:
      app: store
```

When a pod moves, one of two things happens. It lands on a node that
holds a copy from an earlier pod, and it finds that copy's data under
`/data`. Or it lands on a node that has never held this volume, and it
finds an empty directory there and catches up from its peers. Whether
a copy has caught up is the workload's fact, and its readiness probe is
where that belongs. The driver has no caught-up signal.

Each node keeps its copy after the pod leaves. To remove every copy,
delete the `PersistentVolume`. The [volume reference](../../reference/volume/)
says what deleting removes.
