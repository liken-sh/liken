---
title: Manual
---

# The `display-operator` manual

The guides show how to install `display-operator`, put a pod's window
on a screen, divide a screen between several pods, and take a
screenshot. The reference describes the devices and their attributes,
what a claim gives a container, the `Display` resource that holds each
panel's controls, the `Layout` resource, the HTTP API, and the
metrics.

A workload claims a screen the same way that
[Give a workload a device](https://liken.sh/docs/guides/devices/)
shows for the devices that `liken` publishes, through a device class
such as `display-output`.

This site also serves the manifests that the guides apply, as raw
YAML under [`/deploy/`](/deploy/kustomization.yaml). They're the
repository's own files.

The manual covers how to run the operator. The
[source](https://github.com/liken-sh/liken/tree/main/display-operator)
explains how it works, in the comments of its Go files and manifests,
and the
[design documents](https://github.com/liken-sh/liken/tree/main/display-operator/plans)
explain why it's built this way.
