# A connected controller can have no input

Open problem. BlueZ can report a Bluetooth LE controller as connected
while no HID device exists for it. The operator then publishes the
controller with no taint, and nothing reports a fault. Every press
the controller sends is lost until somebody restarts bluetoothd.

## What happened

The incident used a T6 remote, a Bluetooth LE remote that sends its
keys over HID-over-GATT. It was bonded to a machine with a Realtek
radio, which ran bluetooth-operator `2026.09.30-001` and bluetoothd
5.82. All times are UTC on 2026-10-02.

A good reconnect at 11:58:16 logged these lines in the same second:

```
operator  controller FE:5C:63:A3:FF:2E: hid add
kernel    input: T6-Remote Keyboard as /devices/virtual/misc/uhid/0005:620A:0407.0007/input/input21
kernel    input: T6-Remote Mouse as /devices/virtual/misc/uhid/0005:620A:0407.0007/input/input22
kernel    hid-generic 0005:620A:0407.0007: input,hidraw0: BLUETOOTH HID v0.00 Keyboard [T6-Remote] on 88:3b:dc:f7:42:7e
kernel    Bluetooth: hci0: unexpected cc 0x0c7c length: 1 < 3
```

The remote went to sleep at 13:26:25. The operator logged
`hid remove` and applied the `bluetooth.liken.sh/disconnected` taint.
`bluetooth_disconnects_total` increased by one. This is the normal
cycle, and it happened several times each day.

At 17:04:45 the operator removed the taint, because BlueZ reported
the device as connected again. The `Peripheral` condition `Connected`
went to `True` with the reason `LinkUp`. But no `hid add` followed.
The kernel created no uhid device, and the kernel and bluetoothd
logged no lines at all between 13:20 and 17:06.

For the next 5 hours and 33 minutes, no input arrived. During the
test at the end of that time, the remote's light flashed on each
press:

* `bluetooth_input_events_total` for the peripheral stayed at 9287,
  its value since 12:28.
* A reader on the two virtual input nodes in the consumer's pod
  received zero bytes over 60 seconds of presses.
* The consumer published no key events.

A restart of the operator pod at 22:37:46 stopped bluetoothd and
dropped the link. The operator published the controller with the
`disconnected` taint. At 22:38:13, the next press reconnected the
remote, and the operator and the kernel logged the same lines as the
good reconnect at 11:58.

## What is not known

The logs do not show why the HID-over-GATT profile did not start at
17:04. bluetoothd ran without `-d`, and no `btmon` trace ran. Two
explanations fit the logs, and neither is proven:

* The link came back, but encryption or GATT discovery did not
  complete, so the `input-hog` profile did not start. This
  explanation assumes that the profile waits for an encrypted link.
  Nobody has read BlueZ's source to confirm it.
* bluetoothd kept state for the device from the drop at 13:26, and
  it did not start the profile again on the new link.

The fault occurred one time. Nobody has made it happen again.

## Why the operator publishes no fault

The operator derives the taints from two inputs, and neither input
changed in this state:

* `disconnectedTaint` follows BlueZ's `Device1.Connected` property,
  which `bluez.go` reads. BlueZ reported `true`.
* `noInputNodeTaint` follows the relay's virtual nodes, from
  `virtualNodes` in `relay.go`. The relay keeps a controller's
  virtual devices open across a disconnect, so that a consumer
  keeps the node numbers it received. So the virtual nodes existed,
  although no real node fed them.

So the slice published the controller with no taint, and the
`Peripheral` reported `Connected: True`. A connected controller with
no real input node is a state that the operator does not model.

## What a fix needs to decide

The operator receives a `hid add` uevent for each real HID device,
so it can detect a link that has been connected for some time with
no real node. The open questions:

* The time limit. On the two good reconnects in this incident,
  `hid add` came 2 to 3 seconds before the operator removed the
  `disconnected` taint. The time from the link to `hid add` is not
  measured, so the limit is a guess.
* What the operator does after the limit. It can report the state
  only, with a taint or a `Peripheral` condition. Or it can also
  repair the link with `Device1.Disconnect`, so that the next press
  reconnects. A disconnect repairs the fault only if the explanation
  is a profile that did not start on this link. If bluetoothd kept
  stale state, only a restart of bluetoothd clears it.
* Which controllers the check applies to. A bond that has never
  connected has no real node, and `noInputNodeTaint` already reports
  it. The check applies to a controller that registered a real node
  before. Only one Bluetooth LE remote has shown the fault, and
  nobody has seen it on a BR/EDR controller.
