---
title: Players, plays, and their pods
weight: 10
---

# Players, plays, and their pods

You describe your equipment once, and then start and stop media by
creating and deleting objects:

* A `Player` describes one unit of equipment. It selects its screen,
  speakers, and GPU from the devices that the hardware operators
  publish. It uses the same
  [CEL](https://kubernetes.io/docs/reference/using-api/cel/) selectors
  as a `ResourceClaim` that you write by hand. You write one for each
  unit of equipment and rarely change it.
* A `Remote` describes one controller, and a `Keymap` describes the
  buttons of one controller model. You write a `Remote` for each
  controller. You write a `Keymap` only for a model whose buttons the
  base mapping gets wrong.
* A `Play` is one run of media. Create it to start the media and
  delete it to stop. When a `Play` finishes, the operator deletes it
  after its `ttlSecondsAfterFinished`.

`media-operator` creates one playback pod for each `Play`, on the
machine that has the hardware. The pod claims the speakers and the
other devices only while the `Play` runs. Between runs, a `Player`
claims only its screen, and its GPU render node when `spec.render` is
set, so other workloads can use its other devices.

## What runs

The install creates these `Deployment`s in `liken-system`:
`media-operator`, the media API `media-api`, and the message bus `bus`,
which runs one Mosquitto broker. It also creates the `DaemonSet`
`media-capabilities`, whose pod publishes what a node's GPU media
driver can decode, encode, and scale. On a node with no GPU the pod
stays `Pending`. Label that node `media.liken.sh/gpu: none` and it gets
no pod.

The operator creates two more kinds of long-running pod. For each
`Player` whose idle screen `media-operator` draws, it creates an idle
pod that draws the screen while nothing plays. For each `Remote`, it
creates a pod that reads the controller and publishes its button
presses to the message bus.
