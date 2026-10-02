---
title: Guides
weight: 10
---

# Guides

The guides give the steps to operate this project: the
[install](/docs/guides/install/),
[declaring a Player](/docs/guides/declare-a-player/) for a screen,
[mapping a controller](/docs/guides/mapping-a-controller/) whose
codes no one has written down,
[handing the idle screen to another controller](/docs/guides/handing-the-idle-screen-to-another-controller/),
[recording what a player is playing](/docs/guides/record/), and
[claiming a GPU by what it decodes](/docs/guides/claim-a-gpu-by-capability/).
After those guides, declare the resources you need. The
[reference](/docs/reference/) describes each resource.

## How the pieces fit

The install puts two `Deployments` in `liken-system`: the operator
and the message bus, one Mosquitto broker. It also puts one
`DaemonSet` there: the capabilities agent, one pod on each node with
a GPU, which publishes what each GPU's media driver can do. Nothing
else runs continuously.

The resources divide the work by how often you write them. A
`Player` is written once per unit of equipment. It selects the
unit's devices out of what the hardware operators publish, with the
same [CEL](https://kubernetes.io/docs/reference/using-api/cel/)
selectors a hand-written `ResourceClaim` would use. A `Remote` and
its `Keymap` are written once per controller. A `Play` is written
per run: create it to start media on a player, and delete it to
stop. The operator deletes a finished `Play` after its
`ttlSecondsAfterFinished`.

The operator turns a `Play` into one playback pod beside the
hardware. It creates the other device claims only while that `Play`
runs. An idle `Player` keeps only its display claim, so other
workloads can use the remaining devices between runs.
