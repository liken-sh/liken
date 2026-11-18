---
title: How a room is controlled
weight: 10
---

# How a room is controlled

A room is a `Player` and the equipment it plays through: an A/V
receiver, a TV, or both. `media-operator` writes a session into the
status of the `Receiver` that the `Player` plays through. It does this
when the `Player` starts playing, wakes its screen, or turns off, and
when someone presses a volume key. `equipment-operator` reads the
session and sends the receiver its commands. It also finds the
`Television` that shows the session's `Display` and copies the session
into that `Television`'s status, so the TV wakes and goes to standby
with the room.

The operators connect to no message bus. They pass everything through
these fields:

* The session in the `Receiver`'s status. `media-operator` writes it.
* The `Receiver`'s `spec.volume.indicator`, which says whether the
  `Player`'s screens draw the volume bar or the receiver shows its own.
  `media-operator` reads it.
* The `Television`'s `status.screenAsk`. `equipment-operator` writes it
  when the TV should wake the `Player`'s screen or put it to sleep, for
  example when someone picks the machine's input in the TV's source
  menu. `media-operator` passes it on to the `Player`'s screen.

The operator sends the receiver a power or input command only when the
receiver reports a value that differs from the session's. So a `Play`
sends nothing to a receiver that is already on the right input. After
a restart, the operator sends nothing for the sessions it finds.

## Receivers

A `Receiver` is an A/V receiver on your network. Its spec says how to
reach it, which input each `liken` machine is connected to, and the
volume limits for the room's remote. The operator controls the receiver
over its own network protocol: the Denon and Marantz control protocol,
or WiiM's HTTPS API. The operator finds WiiM devices on the network and
creates a `Receiver` for each one. You declare a Denon or Marantz
receiver yourself.

A WiiM amp sends its volume, mute, and play state to the operator as
UPnP events, so a change made at the amp shows in the `Receiver` as
soon as the event arrives. The amp sends no events for its identity and
its input, so the operator reads them every 10 seconds. That read also
tells the operator when the amp is gone.

The amp's settings, such as the equalizer, the balance, and the timers,
change only when a person changes them. The operator reads them when it
starts, after it sends a setting, after the amp comes back, and every
30 minutes. So a setting changed in the WiiM Home app can take up to 30
minutes to show in `status.wiim`. The amp's temperatures and Wi-Fi
readings change on their own, so the operator reports them as
[metrics](/docs/reference/metrics/) and not in status.

## Televisions and the CEC bus

HDMI-CEC is a control wire that every HDMI device in the room shares. A
USB CEC adapter, plugged into a `liken` machine and into a spare input
of the receiver, connects that machine to the wire. A `CECBus` is that
wire and the adapters on it. Each adapter is run by a node workload,
the pod of the `equipment-operator-cec` `DaemonSet` on the adapter's
machine. The node workload creates a `CECBus` for each adapter that no
`CECBus` names.

A `CECBus` starts in `Listen`, where the adapter hears the bus and
sends nothing. When you set a `CECBus` to `Control`, the adapter joins
the bus as a playback device, announces the machine's physical address,
and finds the devices on the bus. The operator creates a `Television`
for each TV it finds. From then on the adapter does these things:

* It wakes the TV and switches it to the machine when the room wakes.
* It puts the TV in standby when the room turns off.
* It passes the key presses of the TV's remote to the `Player`.

When the machine's picture passes through a receiver on the bus, the
wake also asks the receiver to turn on System Audio Mode. The TV then
mutes its own speakers and sends the volume keys of its remote to the
receiver, so the TV remote sets the volume that the room hears.

The adapter scans the bus once, when it joins, and sends nothing on a
timer after that. It sends a device a query only when that device
announces itself, because a TV can switch its own input in reply to an
adapter that queries it too often.
