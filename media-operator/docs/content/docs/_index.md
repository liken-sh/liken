---
title: Manual
---

# The `media-operator` manual

The guides show how to install `media-operator`, declare your units of
equipment, and play media on them. The reference describes each resource, its fields,
and its topics on the [message bus](/docs/reference/bus/).

The operator turns players, plays, remotes, and keymaps into pods
that claim the devices that the other operators publish:
[screens](https://liken.sh/display/),
[audio outputs](https://liken.sh/audio/), and
[Bluetooth controllers](https://liken.sh/bluetooth/). At run time,
every message between the operator and its pods goes over one MQTT
broker. The reference documents that bus, and your own programs can
use it too.

This site also serves the manifests that the guides apply, as raw
YAML under [`/deploy/`](/deploy/kustomization.yaml). They're the
repository's own files.

The manual covers how to run the operator. The
[source](https://github.com/liken-sh/liken/tree/main/media-operator)
explains how it works, in the comments of its Go files and manifests,
and the
[design documents](https://github.com/liken-sh/liken/tree/main/media-operator/plans)
explain why it's built this way.
