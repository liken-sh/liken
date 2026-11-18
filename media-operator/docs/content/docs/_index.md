---
title: Manual
---

# The `media-operator` manual

The guides show how to install `media-operator`, declare the equipment
that plays media, and play media on it. The reference describes each
resource, its fields, and its topics on the
[message bus](/docs/reference/bus/).

The operator turns `Player`, `Play`, `Remote`, and `Keymap` resources
into pods that claim devices from other operators:
[screens](https://liken.sh/display/),
[audio outputs](https://liken.sh/audio/), and
[Bluetooth controllers](https://liken.sh/bluetooth/). The operator and
its pods send all their messages through one MQTT broker, and your own
programs can use it too.

This site also serves the manifests that the guides apply, as raw
YAML under [`/deploy/`](/deploy/kustomization.yaml).

This manual covers how to run the operator. The comments in the
[source](https://github.com/liken-sh/liken/tree/main/media-operator)
explain how it works, and the
[plans](https://github.com/liken-sh/liken/tree/main/media-operator/plans)
give the reasons for its design.
