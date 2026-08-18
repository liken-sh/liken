---
title: Manual
---

# The `audio.liken.sh` manual

This manual tells you how to install `audio-operator` on a
[`liken`](https://liken.sh/docs/) cluster and how to play a
workload's sound through a physical output. The guides give the
steps. The reference describes the devices, their attributes, and
what a claim delivers.

The operator publishes each physical audio output of a machine as a
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
device. A workload claims one through the `audio-output` device
class, the way
[Give a workload a device](https://liken.sh/docs/guides/devices/)
shows for `liken`'s own devices.

This site also serves the deployment manifests the guides apply, as
raw YAML under [`/deploy/`](/deploy/kustomization.yaml). They are
the repository's own files, published with the manual that describes
them.

This manual is small on purpose. The
[repository](https://github.com/liken-sh/audio-operator) is written
to be read: the Go files and the manifests have comments that
explain how the operator works. The manual tells you how to operate
it; the
[design documents](https://github.com/liken-sh/audio-operator/tree/main/plans)
say why it is built the way it is.
