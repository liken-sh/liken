---
title: Manual
---

# The `audio-operator` manual

The guides cover installing `audio-operator`, playing a pod's sound
through an output, pairing a monitor's speakers with its screen,
setting an output's volume and mute, and listening to what an output
plays. The reference describes the devices and their attributes, what
a claim gives a container, the `Sink` and `Source` resources, the
HTTP API and its routes, and the metrics.

A workload claims an output through the `audio-sink` or `audio-source`
device class. The steps are the same as those in
[Give a workload a device](https://liken.sh/docs/guides/devices/),
which uses the devices that `liken` publishes.

This site also serves the manifests that the guides apply, as raw YAML
under [`/deploy/`](/deploy/kustomization.yaml). They are the
repository's own files.

The manual covers how to run the operator. The comments in the Go
files and manifests of the
[source](https://github.com/liken-sh/liken/tree/main/audio-operator)
explain how it works, and the
[design documents](https://github.com/liken-sh/liken/tree/main/audio-operator/plans)
explain why it is built this way.
