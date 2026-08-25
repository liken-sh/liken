---
title: Guides
weight: 10
---

# Guides

The guides give the steps to operate this project. Today there is
one, the [install](/docs/guides/install/), because after the install
the work is declaring resources, and the
[reference](/docs/reference/) describes each one.

## How the pieces fit

The install puts two Deployments in `liken-system`: the operator and
the message bus, one Mosquitto broker. Nothing else is standing
infrastructure.

The resources divide the work by how often you write them. A
`Player` is written once per unit of equipment: it selects the
unit's devices out of what the hardware operators publish, with the
same [CEL](https://kubernetes.io/docs/reference/using-api/cel/)
selectors a hand-written `ResourceClaim` would use. A `Remote` and
its `Keymap` are written once per controller. A `Play` is written
per run: create it to start media on a player, and delete it to
stop. The operator deletes a finished `Play` after its
`ttlSecondsAfterFinished`.

The operator turns a `Play` into one playback pod beside the
hardware, and it creates the device claims only while that `Play`
runs. An idle `Player` holds no claims except its idle display, so
the equipment is free for any other workload between runs.
