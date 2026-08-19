---
title: Manual
---

# The `bluetooth.liken.sh` manual

This manual tells you how to install `bluetooth-operator` on a
[`liken`](https://liken.sh/docs/) cluster and how to pair a
controller and give it to a pod. The guides give the steps. The
reference describes the devices, their attributes and taints, the
pairing API, and what a claim delivers.

The operator publishes each paired Bluetooth device as a
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
device. A workload claims an input device through the
`bluetooth-input` device class, the way
[Give a workload a device](https://liken.sh/docs/guides/devices/)
shows for `liken`'s own devices.

One published device is not a paired peer: the adapter's [media
bus](/docs/reference/devices/#the-media-bus), which the machine's
sound server claims to serve Bluetooth speakers.

This site also serves the deployment manifests the guides apply, as
raw YAML under [`/deploy/`](/deploy/kustomization.yaml): the
[CRDs](/deploy/crds.yaml), the [RBAC](/deploy/rbac.yaml), and the
[workload](/deploy/operator.yaml). They are the repository's own
files, published with the manual that describes them. The
[`bluetooth-adapter` class](/deploy/deviceclasses.yaml) ships among
them, because the operator's own claim template names it. The
`bluetooth-input` class does not: a class workloads claim through is
cluster policy, yours to create, and the install guide gives its
YAML.

This manual is small on purpose. The
[repository](https://github.com/liken-sh/bluetooth-operator) is
written to be read: the Go files and the manifests have comments
that explain how the operator works. The manual tells you how to
operate it; the
[design documents](https://github.com/liken-sh/bluetooth-operator/tree/main/plans)
say why it is built the way it is.

Every page of this site is also available as Markdown. Add `index.md`
to a page's address to get it.
