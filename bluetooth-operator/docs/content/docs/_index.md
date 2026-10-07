---
title: Manual
---

# The `bluetooth-operator` manual

The guides show how to install `bluetooth-operator`, pair a
controller, and give it to a pod. The reference describes the pairing
API, the devices with their attributes and taints, what a claim gives
a container, and the metrics.

A workload claims a paired controller the same way that
[Give a workload a device](https://liken.sh/docs/guides/devices/)
shows for the devices that `liken` publishes, through the
`bluetooth-input` device class. The operator also publishes one device
that isn't a paired peer: the radio's
[media bus](/docs/reference/devices/#the-media-bus), which
[`audio-operator`](https://liken.sh/audio/) claims to play to
Bluetooth speakers.

This site also serves the manifests that the guides apply, as raw
YAML under [`/deploy/`](/deploy/kustomization.yaml). They're the
repository's own files. They include the `bluetooth-adapter` class,
because the operator's own pod claims the radio through it, and not
the `bluetooth-input` class, because the classes your workloads
claim through are yours to create. The install guide gives its YAML.

The manual covers how to run the operator. The
[source](https://github.com/liken-sh/liken/tree/main/bluetooth-operator)
explains how it works, in the comments of its Go files and manifests,
and the
[design documents](https://github.com/liken-sh/liken/tree/main/bluetooth-operator/plans)
explain why it's built this way.

Every page of this site is also available as Markdown. Add `index.md`
to a page's address to get it.
