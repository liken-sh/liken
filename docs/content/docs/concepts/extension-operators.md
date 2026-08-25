---
title: The extension operators
weight: 20
aliases:
  - /docs/concepts/hardware-operators/
---

# The extension operators

The extension operators are operators that you install on a `liken`
cluster. Each one is its own project, with its own repository and
its own manual on a `liken.sh` subdomain, and a cluster installs
only the ones its equipment and its workloads need. A cluster that
installs none of them runs unchanged.

Two families exist today. The hardware operators publish the
machine's hardware as devices a workload can claim. The media
operator composes those devices into media playback. The families
layer in one direction: the media operator claims devices the way
any workload does, and no hardware operator knows what runs above
it.

## The hardware operators

Each hardware operator publishes one kind of machine hardware as
DRA devices, so a workload claims that hardware the way
[Give a workload a device](/docs/guides/devices/) shows, with no
privilege and no host path.

Each operator is a DRA driver of its own, separate from the
operating system's driver that [Devices](/docs/reference/devices/)
describes. The operating system publishes the hardware a machine
holds, such as a Bluetooth radio, a GPU, or a sound card. A hardware
operator publishes what that hardware serves, at the grain a
workload asks for: a paired controller, a monitor output, an audio
output.

A device can also take parameters from the claim. A `ResourceClaim`
carries an opaque config block per driver, the operator reads it
when it prepares the claim, and it puts the device into the state
the block asks for before the pod starts. The audio operator takes
`codec` on a Bluetooth speaker, and the display operator takes
`mode` on a monitor output. A `DeviceClass` can carry the same
block as cluster policy, and the claim's own block wins. Each
operator's manual documents its parameters beside its attributes.

The name of a hardware operator's device class is also the hostname
of its manual. Each manual gives the install steps and the claims
for its devices:

* [bluetooth.liken.sh](https://bluetooth.liken.sh) publishes paired
  Bluetooth controllers. The source is
  [liken-sh/bluetooth-operator](https://github.com/liken-sh/bluetooth-operator).
* [display.liken.sh](https://display.liken.sh) publishes monitor
  outputs. The source is
  [liken-sh/display-operator](https://github.com/liken-sh/display-operator).
* [audio.liken.sh](https://audio.liken.sh) publishes audio outputs.
  The source is
  [liken-sh/audio-operator](https://github.com/liken-sh/audio-operator).

## The media operator

The media operator is the routing and control of media playback on
a cluster: which display and speakers form a unit, what plays on
it, and which controller drives it, all declared as Kubernetes
resources. It publishes no devices of its own. It selects devices
out of what the hardware operators publish, with the same CEL
selectors a hand-written `ResourceClaim` would use, and it claims
them only for the pods it runs.

Its manual is [media.liken.sh](https://media.liken.sh): the
resources, the install, and the MQTT message bus its pods and your
own programs share. The source is
[liken-sh/media-operator](https://github.com/liken-sh/media-operator).
