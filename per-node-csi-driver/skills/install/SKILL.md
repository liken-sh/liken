---
name: install
description: "Install per-node-csi-driver from its kustomize base with the per-node StorageClass, and set the plugin's flags. Use when a cluster needs one directory per node per volume."
---

This skill is the guide at https://per-node.liken.sh/docs/guides/install/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

Install the driver from the kustomize base in the repository's
`deploy/` directory. You need a cluster with standard CSI plumbing,
`kubectl` with cluster-admin rights, and the `liken-system` namespace.

Add the base to your own kustomization and pin `<tag>` to a release,
so the install is the same every time you apply it. The base creates
the `CSIDriver` object, the `StorageClass` named `per-node`, the
`ServiceAccount` and its role, and the `DaemonSet` that runs the node
plugin beside the kubelet's registrar on every node. There is no
controller `Deployment`, because the driver provisions nothing.

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: liken-system

resources:
  - https://github.com/liken-sh/per-node-csi-driver//deploy?ref=<tag>

images:
  - name: ghcr.io/liken-sh/per-node-csi-driver
    newTag: <tag>
```

Check that every node lists `per-node.liken.sh` among its drivers. A
node appears there after the registrar tells its kubelet about the
plugin and the kubelet calls the plugin. A node missing from the answer
has no plugin pod running yet.

```console
kubectl get csinode -o custom-columns=NODE:.metadata.name,DRIVERS:.spec.drivers[*].name
```

## The plugin's flags

The `DaemonSet` passes these flags to the node plugin. Change one
through a kustomize patch on the container's `args`.

| Flag | Default | Meaning |
|---|---|---|
| `--endpoint` | `unix:///csi/csi.sock` | The socket the kubelet and the registrar call. |
| `--node-id` | none | The node's name, which the base takes from the pod's `spec.nodeName`. Required. |
| `--store` | `/var/lib/liken/pod-storage/per-node` | Where the node plugin keeps its copies and its holds. On `liken` this is the pod-storage partition. |
| `--metrics` | `:9200` | Where the node plugin serves its Prometheus metrics. An empty value serves none. |
| `--sweep-every` | `10m` | How often the driver looks for a copy that no `PersistentVolume` names. The driver acts on a deletion at once, and this pass finds what the watch missed. |
| `--version` | none | Print the version and exit. |
