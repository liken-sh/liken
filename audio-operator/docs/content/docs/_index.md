---
title: Manual
---

# The `audio-operator` manual

The guides show how to install `audio-operator`, play a pod's sound
through an output, pair a monitor's speakers with its screen, set an
output's volume and mute, and listen to what an output plays. The
reference describes the devices and their attributes, what a claim
gives a container, the `Sink` and `Source` resources, and the HTTP
API.

A workload claims an output the same way that
[Give a workload a device](https://liken.sh/docs/guides/devices/)
shows for the devices that `liken` publishes, through the
`audio-sink` or `audio-source` device class.

This site also serves the manifests that the guides apply, as raw
YAML under [`/deploy/`](/deploy/kustomization.yaml). They're the
repository's own files.

The manual covers how to run the operator. The
[source](https://github.com/liken-sh/liken/tree/main/audio-operator)
explains how it works, in the comments of its Go files and manifests,
and the
[design documents](https://github.com/liken-sh/liken/tree/main/audio-operator/plans)
explain why it's built this way.
