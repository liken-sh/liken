---
title: How a room is controlled
weight: 10
---

# How a room is controlled

A room is a `Player` and the equipment it plays through: an A/V
receiver, a TV, or both. When a `Player` starts playing, wakes its
screen, or gets a press of a volume or power key, `media-operator`
writes a session into the status of the `Receiver` that the `Player`
plays through. `equipment-operator` reads the session and sends the
receiver its commands. It also finds the `Television` that shows the
session's `Display` and writes the session into that `Television`'s
status, so the TV wakes and goes to standby with the room.

The two operators share no message bus. They talk through three
fields: the session in the `Receiver`'s status, which `media-operator`
writes, the `Receiver`'s `spec.volume.indicator`, which
`media-operator` reads, and the `Television`'s `status.screenAsk`,
which `equipment-operator` writes when the TV should wake or put the
`Player`'s screen to sleep, such as when a person picks the machine's
input in the TV's source menu, and which `media-operator` passes to
the `Player`'s screen.

The operator sends the receiver's power and input only when the
receiver reports a different value from the session's. So when a
receiver is already on the right input, a `Play` sends it nothing, and
an operator restart sends nothing for the sessions it finds.

## Receivers

A `Receiver` is an A/V receiver on your network. Its spec says how to
reach it, which of its inputs each `liken` machine is connected to,
and the volume limits for the room's remote. The operator controls
it over the receiver's own network protocol: the Denon and Marantz
control protocol, or WiiM's HTTPS API. The operator finds WiiM
devices on the network and creates a `Receiver` for each one. You
declare a Denon or Marantz receiver yourself.

## Televisions and the CEC bus

HDMI-CEC is one wire that every HDMI port in the room shares. A
USB-CEC adapter plugged into a `liken` machine and into a spare input
of the receiver connects that machine to the wire. Each adapter
appears as a `CECBus`.

A `CECBus` starts in `Listen`: it hears the bus and sends nothing.
When you declare your own `CECBus` in `Control`, the adapter joins the
bus as a playback device, announces the machine's video input, and
finds the devices on the bus. For each TV it finds, the operator
creates a `Television`. From then on, the adapter wakes the TV and
switches it to the machine when the room wakes, puts it in standby
when the room turns off, and passes the TV remote's key presses to
the `Player`.

The adapter scans the bus once when it joins, and after that sends
nothing on a timer. It asks a device questions only when that device
announces itself, because a TV can switch its own input in reply to an adapter
that asks too often.
