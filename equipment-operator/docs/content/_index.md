---
title: equipment-operator
---

# `equipment-operator`

`equipment-operator` controls the TV and the A/V receiver that a
[`liken`](https://liken.sh/docs/) machine plays through. When a
`Player` starts playing, it wakes the TV, switches the TV and the
receiver to the machine's input, and turns the receiver on. The
`Player`'s remote then sets the receiver's volume, and its power
button turns the TV and the receiver off.

* A `Receiver` is an A/V receiver on your network. The operator
  reports its power, input, and volume, and controls it while a
  `Player` plays through it. It supports receivers that speak the
  Denon and Marantz control protocol, and WiiM devices.
* A `Television` is a TV on the HDMI-CEC bus. You connect the bus to
  the machine with a USB-CEC adapter, and the operator creates a
  `CECBus` for the adapter. When you set the `CECBus` to `Control`, the
  operator creates a `Television` for each TV on the bus. It then wakes
  the TV when the `Player` starts playing, puts the TV in standby when
  the `Player` turns off, and sends the key presses of the TV's own
  remote to the `Player`.

Start here:

* [Install the operator](/docs/guides/install/), declare a `Receiver`,
  and put it under a `Player`.
* [Connect a USB-CEC adapter](/docs/guides/connect-a-cec-adapter/) so
  that the TV wakes when the `Player` plays.
* Reference: [`Receiver`](/docs/reference/receivers/),
  [`CECBus`](/docs/reference/cecbuses/), and
  [`Television`](/docs/reference/televisions/).

`equipment-operator` is one of the extension operators for
[running a home theater](https://liken.sh/docs/concepts/running-a-home-theater/).
It works with [`media-operator`](https://liken.sh/media/), which
tells it when a `Player` plays.

* [The source](https://github.com/liken-sh/liken/tree/main/equipment-operator)
* [The `liken` manual](https://liken.sh/docs/)
