---
title: Install
weight: 10
---

The driver installs from the kustomize base in the repository's
`deploy/` directory. You need a cluster with standard CSI plumbing,
`kubectl` with cluster-admin rights, and the `liken-system` namespace.

Take the base into your own kustomization and pin `<tag>` to a
release, so the install is the same every time it is applied. The base
creates the `CSIDriver` object, the `ServiceAccount`s and their roles,
the `DaemonSet` that runs the node plugin beside the kubelet's
registrar, and the `Deployment` that runs the controller plugin beside
the `external-resizer`, which carries a claim's class change to the
driver.

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: liken-system

resources:
  - https://github.com/liken-sh/git-csi-driver//deploy?ref=<tag>

images:
  - name: ghcr.io/liken-sh/git-csi-driver
    newTag: <tag>
```

Check that every node lists `git.liken.sh` among its drivers. A node
appears there after the registrar told its kubelet about the plugin
and the kubelet called it. A node missing from the answer has no
plugin pod running yet.

```console
kubectl get csinode -o custom-columns=NODE:.metadata.name,DRIVERS:.spec.drivers[*].name
```

## The plugin's flags

The base sets every flag the plugins need. Change one through a
kustomize patch on the container's `args`.

| Flag | Default | Meaning |
|---|---|---|
| `--endpoint` | `unix:///csi/csi.sock` | The socket the kubelet and the sidecars call. |
| `--node-id` | none | The node's name, which the base takes from the pod's `spec.nodeName`. |
| `--store` | `/var/lib/liken/pod-storage/git-csi` | Where the node plugin keeps its bare repositories, trees, and records. On `liken` this is the pod-storage partition. |
| `--metrics` | `:9808` | Where the node plugin serves its Prometheus gauges. |
| `--sweep-after` | `720h` | How long a work tree nothing stages is kept, and how old an object no ref names has to be before `git gc` prunes it. |
| `--controller` | off | Run the controller plugin instead of the node plugin. |
| `--version` | | Print the version and exit. |
