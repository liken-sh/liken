# Battery levels are not reported

Open problem. A remote, a gamepad, and a pair of earbuds each run on a
battery, and each one can report its level over Bluetooth. This
operator reads none of them. The `battery` profile attribute says a
device advertises the GATT Battery Service, and that is the whole of
what a person can learn from the cluster about a device's charge.

## Where a level comes from

Two sources exist on a `liken` machine, and a device may offer either:

* BlueZ's `org.bluez.Battery1` interface on the device object, with a
  `Percentage` property. BlueZ fills it from the GATT Battery Service on
  an LE device, and from HID battery reports on a device that sends
  them.
* The kernel's power supply class, under `/sys/class/power_supply/`.
  The HID input driver registers one supply per HID device that
  reports battery strength, and the PlayStation driver registers one
  per DualSense.

Which source each device on the testbed exposes has not been checked,
and the answer decides what the operator reads. A device that shows in
both should be read once.

## Where a level goes

A level changes often and selects nothing, so a device attribute on
the `ResourceSlice` is the wrong home: every change would rewrite the
slice, and a claim never selects on charge. Two homes fit:

* The media bus. The operator already publishes each device's
  presence there, retained, and a level beside it reaches every
  consumer that reads presence today: the standing remote pod, the
  idle screen, and whatever a later plan adds.
* The consumers' statuses. `media-operator` owns `Remote.status` and
  `audio-operator` owns `Sink.status`. Each can fold the level it reads
  off the bus into the status of the object that names the device, so
  `kubectl get remotes` and `kubectl get sinks` answer the question
  without a screen.

This operator publishes no per-device object of its own, so a status
here means the bus. The pairing API's status could carry a level for
the device it paired, but a level is a fact of the device's whole
life, not of its pairing.

## What a consumer does with it

The idle screen lists the unit's parts by name. A level beside each
part that has one is the first use, and `media-operator`'s open
problem on the idle screen names it. The browser that
`library-operator` puts on a delegated screen draws the same parts
list in time, and its open problem names the same level.

A low level is also a fact worth a warning, and a warning wants a
threshold and a place to show. That is a design of its own.
