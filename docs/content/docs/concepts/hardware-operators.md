---
title: The hardware operators
weight: 20
---

# The hardware operators

The hardware operators are operators that you install on a `liken`
cluster. Each one publishes one kind of machine hardware as DRA
devices, so a workload claims that hardware the way
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

The name of an operator's device class is also the hostname of its
manual. Each manual gives the install steps and the claims for its
devices:

* [bluetooth.liken.sh](https://bluetooth.liken.sh) publishes paired
  Bluetooth controllers. The source is
  [liken-sh/bluetooth-operator](https://github.com/liken-sh/bluetooth-operator).
* [display.liken.sh](https://display.liken.sh) publishes monitor
  outputs. The source is
  [liken-sh/display-operator](https://github.com/liken-sh/display-operator).
* [audio.liken.sh](https://audio.liken.sh) publishes audio outputs.
  The source is
  [liken-sh/audio-operator](https://github.com/liken-sh/audio-operator).
