---
title: Manual
---

# The `audio.liken.sh` manual

This manual tells you how to install `audio-operator` on a
[`liken`](https://liken.sh/docs/) cluster, how to play a workload's
sound through a physical output, and how to set what an output
rests at. The guides give the steps. The reference describes the
devices, their attributes, what a claim delivers, and the `Sink`
and `Source` resources.

The operator publishes each physical audio endpoint of a machine as
a
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
device, and as a `Sink` or `Source` resource of its own. A workload
claims one through the `audio-sink` or `audio-source` device class,
the way
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
