---
title: Manual
---

# The `media.liken.sh` manual

This manual tells you how to install `media-operator` on a
[`liken`](https://liken.sh/docs/) cluster and how to run media on
the equipment it declares. The guides give the steps. The reference
describes each resource, its fields, and the topics it speaks on the
[message bus](/docs/reference/bus/).

The operator declares players, plays, remotes, and keymaps as
Kubernetes resources, and it reconciles them into pods that claim
the hardware operators' devices:
[displays](https://display.liken.sh),
[audio outputs](https://audio.liken.sh), and
[Bluetooth controllers](https://bluetooth.liken.sh). At run time,
every message between the operator and its pods goes over one MQTT
broker, and the reference documents that bus as the contract your
own programs can join.

This site also serves the deployment manifests the guides apply, as
raw YAML under [`/deploy/`](/deploy/kustomization.yaml). They are
the repository's own files, published with the manual that describes
them.

This manual is small on purpose. The
[repository](https://github.com/liken-sh/media-operator) is written
to be read: the Go files and the manifests have comments that
explain how the operator works. The manual tells you how to operate
it; the
[design documents](https://github.com/liken-sh/media-operator/tree/main/plans)
say why it is built the way it is.
