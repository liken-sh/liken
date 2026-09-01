# BLE devices connect on demand

Open problem. A Bluetooth Low Energy remote drops its link when it is
idle and brings it back on the first press. This operator reads BlueZ's
`Connected` property and reports a device that is not connected as
disconnected, so such a remote reads as off the air for most of its
life, though a press reaches the unit within a moment. The question is
whether these devices need a looser state than `connected`, and what
that state is called.

## What the operator reports today

Two taints carry a controller's health on its published device, and
both key on the link:

* `bluetooth.liken.sh/disconnected`, `NoExecute`, on every controller
  whose `Connected` is false. A consumer tolerates it for as long as it
  likes; `media-operator` tolerates it with no limit, so a sleeping
  controller keeps its allocation.
* `bluetooth.liken.sh/no-input-node`, `NoSchedule`, on every controller
  with no evdev node. No consumer tolerates it, so a claim on a
  controller that has never connected parks `Pending` until it does.

`connected` also travels as a fact: the standing remote pod publishes
presence on the bus, and the idle screen shows each controller as
connected or not.

## What a BLE remote does

A BLE HID remote, one that speaks HID over GATT, keeps its battery by
dropping the link after a short idle. It stays bonded. On a press it
advertises again, BlueZ reconnects the bonded device, the kernel
creates the input node again, and the press arrives. The whole cycle
takes well under a second, and a person notices nothing.

The studio remote on the testbed works this way. Between presses BlueZ
reports it not connected, its device carries the disconnected taint,
the idle screen shows it disconnected, and its input node is gone. So
its claim parks `Pending` under the `no-input-node` taint whenever the
remote pod has to be scheduled while the remote sleeps, which is what
the testbed showed on 2026-09-01: `studio-remote-remote-devices`
`pending` for hours, with a working remote in the room.

A classic Bluetooth controller, a gamepad on BR/EDR, does the opposite.
It holds its link while it is on and drops it only when it is switched
off or out of range, so for it `connected` means what the taint says.

## The questions

* **What is the state?** BlueZ has no property for "bonded and will
  reconnect on demand"; it has `Paired`, `Bonded`, `Connected`, and
  `AddressType`. The term the specification uses for the remote's side
  is connectable advertising by a bonded peripheral, and the central's
  side is reconnection. A word this project could publish is
  `available`: bonded, seen recently, and expected to answer a press,
  as against `connected`, which is a live link now.
* **How is a device told apart?** The candidates are `AddressType`
  (`public` or `random` on LE), the HID over GATT service UUID in
  `UUIDs`, and observed behavior: a device that reconnects on its own
  after a drop. None of them is certain alone. An LE gamepad may hold
  its link, and a BR/EDR device may not.
* **Which taint changes?** If the disconnected taint keys on `available`
  rather than `connected` for these devices, a sleeping remote carries
  no taint, which matches what a person expects. The `no-input-node`
  taint is the harder one: the node is really absent while the remote
  sleeps, and a claim prepared then delivers nothing. A pod that starts
  while the remote sleeps needs the node to appear on the first press
  after it, which is a question for `NodePrepareResources` and for the
  `/dev/uhid` node the claim delivers.
* **What does the idle screen show?** `media-operator` draws
  `connected` per controller from the bus. An `available` state wants a
  third rendering, or it wants `available` folded into `connected` for
  the screen and kept apart in the attributes.

## What is not in question

The device stays published while it sleeps. Membership in the slice
answers "is there such a controller", and that answer is yes. The
change, if there is one, is in the taints and the attributes.
