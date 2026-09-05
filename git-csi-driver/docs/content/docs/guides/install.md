---
title: Install
weight: 10
---

The driver installs from the kustomize base in the repository's
`deploy/` directory. You need a cluster with standard CSI plumbing,
`kubectl` with cluster-admin rights, and the `liken-system` namespace.

Take the base into your own kustomization and pin `<tag>` to a
release, so the install is the same every time it is applied. The base
creates the `CSIDriver` object, the `ServiceAccount`, and the
`DaemonSet` that runs the node plugin.

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
