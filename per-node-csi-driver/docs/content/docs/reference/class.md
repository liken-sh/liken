---
title: The StorageClass
weight: 20
---

# The `per-node` `StorageClass`

The kustomize base in `deploy/` defines the `StorageClass` named
`per-node`. It has no parameters. A volume of this driver gets all of
its configuration from its `PersistentVolume`.

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: per-node
provisioner: per-node.liken.sh
reclaimPolicy: Retain
volumeBindingMode: Immediate
```

The class does not create volumes. A claim that names it stays
`Pending` until a matching `PersistentVolume` exists, because no
controller creates one. Write the `PersistentVolume` first, as the
[volume reference](../volume/) shows.

`Retain` keeps every copy after the claim is deleted. No controller
runs a deleter, so the `PersistentVolume` stays `Released` until you
delete it. `Immediate` binds the claim as soon as a matching
`PersistentVolume` exists. The binding does not wait for a pod,
because every node can serve the volume.
