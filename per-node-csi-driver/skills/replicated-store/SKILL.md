---
name: replicated-store
description: "Run a replicated store as a Deployment on one per-node claim with anti-affinity across nodes and a PodDisruptionBudget. Use when several pods each keep a full copy of one data set and keep the copies in agreement over the network."
---

This skill is the guide at https://liken.sh/per-node/docs/guides/replicated-store/, emitted for agents. Before the first command that reaches a cluster, run `kubectl config current-context` and confirm that it names the cluster the person means. A `liken` command that takes a deployment directory reaches that directory's cluster, whatever the context is.

# Run a replicated store

A replicated store is several pods that each keep a full copy of one
data set and keep the copies in agreement over the network, so a new
copy catches up from the others. On this driver the workload is a
`Deployment` with one `per-node` claim, required anti-affinity across
nodes, and a `PodDisruptionBudget`. It needs no `StatefulSet`, because
no copy has to start before the others.

The driver does not create volumes, so write the `PersistentVolume`
and the claim before you start the pods. Make the handle the same as
the volume's name. The driver does not enforce the capacity. The
`claimRef` binds the volume to this claim and no other. Use the access
mode `ReadWriteMany`, so that pods on many nodes can mount the volume.

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
      # Required anti-affinity allows at most one store pod on each
      # node, so each pod has its own copy. Two pods on one node would
      # share that node's copy. The scheduler counts a terminating pod
      # until it is gone, so a replacement pod does not start on a node
      # where the old pod can still write the copy.
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
  # A new copy catches up from the other copies. When a drain stops
  # only one pod at a time, two of the three copies stay up to serve
  # requests and to bring the replacement up to date.
  maxUnavailable: 1
  selector:
    matchLabels:
      app: store
```

When a pod moves to a node that had a pod of this volume before,
`/data` has the data that the earlier pod left there. On a node that
has never mounted this volume, `/data` is empty, and the pod catches
up from the other pods. The driver cannot tell whether a copy has
caught up, so put that check in the readiness probe of the store
container.

Each node keeps its copy after the pod leaves. To remove every copy,
delete the `PersistentVolume`. The
[volume reference](https://liken.sh/per-node/docs/reference/volume/) describes what deleting
removes.
