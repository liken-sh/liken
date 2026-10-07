---
title: Claiming hardware
weight: 30
---

# Claiming hardware

`display-operator`, `audio-operator`, and `bluetooth-operator` let a
pod claim one monitor output, one speaker, or one game controller,
without a privileged container. The operating system publishes whole
pieces of hardware as devices, such as a sound card, a GPU, or a
Bluetooth radio, as [Devices](/docs/reference/devices/) describes.
A pod usually needs one output of a card, not the whole card. So each
operator claims the hardware for its own pod, and publishes each
output as a device that a pod can claim.

For example, a pod that plays a film can claim one monitor output at
`1280x720` and one Bluetooth speaker with the `aptx` codec. The pod
isn't privileged. Its container gets `WAYLAND_DISPLAY` for the monitor
and `PIPEWIRE_REMOTE` for the speaker, and draws and plays through
those sockets.

Each operator is a DRA driver, with its own `ResourceSlice` on each
node, separate from the operating system's driver, `liken.sh`. A claim
can carry settings for the device in an opaque config block, such as
a monitor's `mode` or a speaker's `codec`. The operator reads the
block when it prepares the claim, and sets the device up that way
before the pod starts. A `DeviceClass` can carry the same block as
cluster policy, and the claim's own block wins.

## The operators

[`display-operator`](https://liken.sh/display/) runs the Weston
compositor on each machine and publishes each monitor output as a
device of the driver `display.liken.sh`. A claim can set the output's
`mode`, `brightness`, and `power`. A `Layout` puts programs from
several namespaces on one monitor, each in its own rectangle, and a
`Display` reports the state of each monitor.

[`audio-operator`](https://liken.sh/audio/) runs PipeWire on each
machine and publishes each physical audio output as a device of the
driver `audio.liken.sh`: the speakers of a monitor, the analog jack, a
USB sound card, and each paired Bluetooth speaker. A claim on a
Bluetooth speaker can set its `codec`. A `Sink` and a `Source` hold
the volume and mute state of each output and input, and you can
change them without a claim and without interrupting a pod that is
playing.

[`bluetooth-operator`](https://liken.sh/bluetooth/) runs BlueZ on each
machine that has a Bluetooth radio, pairs controllers through a
`PairingRequest`, and publishes each paired controller as a device of
the driver `bluetooth.liken.sh`. A claim's `inputs` choose which
kinds of input from the controller reach the container, and its `axes`
tune the sticks.

## What they depend on

Each device operator claims devices that the operating system
publishes, through a `DeviceClass` that it ships for its own pod:

* `display-operator` claims the display node and the render node of
  each GPU, and the I2C buses that reach each monitor's controls.
* `audio-operator` claims every device whose attribute
  `sound.liken.sh/supportsSound` is true. The operating system sets
  that attribute on each sound card.
* `bluetooth-operator` claims each USB Bluetooth radio that the kernel
  driver `btusb` serves.

Some machines need a kernel module before the operating system
publishes the hardware. [Load the drivers a machine
needs](/docs/guides/hardware-modules/) gives the steps.

`display-operator` builds on the project's `weston` image, and its
capture image builds on the `ffmpeg` image.

## Extension points

* Device classes for your workloads. Each operator publishes devices,
  and you write the `DeviceClass` that selects them. Each manual gives
  the attributes that a CEL selector can match.
* The attribute `sound.liken.sh/supportsSound`. The operating system
  sets it on a sound card, and `bluetooth-operator` sets it on the
  media bus of each Bluetooth radio, so `audio-operator` claims both.
  Another DRA driver can set the same attribute on a device it
  publishes, and `audio-operator` claims that device too.
* One monitor with its own speakers. A claim can ask for a monitor
  output and the audio output of the same monitor in one request,
  matched on the attribute `monitor.liken.sh/id`.
* Volume control from another program. A program that writes
  `status.session.volumeAsk` on a `Sink` asks `audio-operator` to
  change the volume within the limits that the `Sink` sets.
  `media-operator` uses it.
* Capture. `kubectl liken display capture` and
  `kubectl liken audio capture` record what a monitor shows and what a
  speaker plays, and each operator serves the same capture over HTTP.
* Raw panel control. A claim on the I2C bus of a monitor gives a pod
  the bus, for a program that speaks DDC/CI to the panel.
