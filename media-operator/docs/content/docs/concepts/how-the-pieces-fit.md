---
title: Players, plays, and their pods
weight: 10
---

# Players, plays, and their pods

You describe your equipment once, and then start and stop media by
creating and deleting objects. Each resource changes at its own pace:

* You write a `Player` once for each unit of equipment. It selects the
  unit's screen, speakers, and GPU from the devices that the other
  operators publish, with the same
  [CEL](https://kubernetes.io/docs/reference/using-api/cel/) selectors
  that a hand-written `ResourceClaim` uses.
* You write a `Remote` once for each controller, and a `Keymap` once
  for each controller model that needs one.
* You write a `Play` for each run of media. Create it to start, and
  delete it to stop. The operator deletes a finished `Play` after its
  `ttlSecondsAfterFinished`.

The operator turns each `Play` into one playback pod on the machine
with the hardware. It claims the speakers and the other devices only
while that `Play` runs. An idle `Player` keeps only its display claim,
so other workloads can use the rest of its devices between runs.

## What runs

The install puts three `Deployments` in `liken-system`: the operator,
the media API, and the message bus, which is one Mosquitto broker. It
also puts one `DaemonSet` there: the capabilities agent, with one pod
on each node with a GPU, which publishes what each GPU's media driver
can do.

The operator then creates two more kinds of long-running pod: an idle
pod for each `Player`, which draws the screen while nothing plays, and
a pod for each `Remote`, which reads the controller and sends its
button presses to the message bus.
