---
title: Install the driver
weight: 10
description: "Install per-node-csi-driver from its kustomize base with the per-node StorageClass, and set the plugin's flags. Use when a cluster needs one directory per node per volume."
---

# Install the driver

Install the driver from the kustomize base in the driver's `deploy/`
directory. You need `kubectl` with cluster-admin rights and the
`liken-system` namespace.

Add the base to your own `kustomization.yaml` and set `<tag>` to the
driver's version, so that every apply installs the same version. The
base creates the `CSIDriver` object, the `StorageClass` named
`per-node`, the `ServiceAccount` and its role, and the `DaemonSet` that
runs the node plugin and the kubelet's registrar on every node. There
is no controller `Deployment`, because the driver does not create
volumes.

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: liken-system

resources:
  - https://github.com/liken-sh/liken//per-node-csi-driver/deploy?ref=<tag>

images:
  - name: ghcr.io/liken-sh/per-node-csi-driver
    newTag: <tag>
```

The driver's version is the tag of the release that last published it. The
[GitHub release](https://github.com/liken-sh/liken/releases) for each
tag lists every component and its version.

Apply the kustomization from its directory:

```console
kubectl apply -k .
```

Then check that every node lists `per-node.liken.sh` among its
drivers. A node that does not list it has no running plugin pod yet.

```console
kubectl get csinode -o custom-columns=NODE:.metadata.name,DRIVERS:.spec.drivers[*].name
```

## The plugin's flags

The `DaemonSet` named `per-node-csi-driver-node` passes these flags
to the node plugin. To change one, patch the `args` of its `driver`
container in your kustomization.

| Flag | Default | Meaning |
|---|---|---|
| `--endpoint` | `unix:///csi/csi.sock` | The socket the kubelet and the registrar call. |
| `--node-id` | none | The node's name, which the base sets from the pod's `spec.nodeName`. Required. |
| `--store` | `/var/lib/liken/pod-storage/per-node` | The directory where the node plugin keeps its copies and its records of which pods mount each copy. On `liken` it is on the pod-storage partition. |
| `--metrics` | `:9200` | Where the node plugin serves its Prometheus metrics. An empty value serves none. |
| `--sweep-every` | `10m` | How often the driver removes the copies that no `PersistentVolume` names. It also removes them as soon as a `PersistentVolume` is deleted, so the interval matters only for a removal that failed, and for a copy that a pod still mounted when its `PersistentVolume` was deleted. |
| `--version` | none | Print the version and exit. |
