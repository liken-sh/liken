---
title: Manual
---

# The `display.liken.sh` manual

This manual tells you how to install `display-operator` on a
[`liken`](https://liken.sh/docs/) cluster and how to put a workload's
window on a screen. The guides give the steps. The reference
describes the devices, their attributes, what a claim delivers, and
the `Display` resource that carries each panel's controls.

The operator publishes each monitor output of a graphics card as a
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
device. A workload claims one through the `display-output` device
class, the way
[Give a workload a device](https://liken.sh/docs/guides/devices/)
shows for `liken`'s own devices.

This site also serves the deployment manifests the guides apply, as
raw YAML under [`/deploy/`](/deploy/kustomization.yaml). They are the
repository's own files, published with the manual that describes
them.

This manual is small on purpose. The
[repository](https://github.com/liken-sh/display-operator) is written
to be read: the Go files and the manifests have comments that
explain how the operator works. The manual tells you how to operate
it; the [design documents](https://github.com/liken-sh/display-operator/tree/main/plans)
say why it is built the way it is.
