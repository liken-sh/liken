---
title: The class
weight: 20
---

The kustomize base in `deploy/` defines the `StorageClass` named
`per-node`. It takes no parameters. A volume of this driver gets all of
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

The class provisions nothing. A claim that names it, with no
`PersistentVolume` written for it, stays `Pending`, because there is no
controller to answer. Write the `PersistentVolume` first, as the
[volume reference](../volume/) shows.

`Retain` keeps every copy after the claim goes: no controller runs a
deleter, and the `PersistentVolume` stays `Released` until a person
deletes it. `Immediate` binds the claim as soon as a matching
`PersistentVolume` exists, which is right because no node is closer to
a volume than another.
