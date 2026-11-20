# Working on bluetooth-operator

`bluetooth-operator` is a Go Kubernetes operator and a DRA driver with
the driver name `bluetooth.liken.sh`. `README.md` says what it is for
and describes its kinds.

## How it runs

One `bluetooth-operator` pod runs on each node that has a radio. Its
claim takes the radio that `liken` publishes for the `btusb` driver.
The pod runs in the host's network namespace, because the kernel
opens `AF_BLUETOOTH` sockets only there.

`bluetoothd` owns the radio. Its container also runs a private
`dbus-daemon`, and puts the socket of that bus in a host path. The
`operator` container is a client of the bus, and so is the WirePlumber
of `audio-operator`, which claims the bus as the media bus device.

The `Secret`s labelled with the adapter's address are the record of
each bond and of the radio's identity key. BlueZ reads its bond tree,
the directory of files where BlueZ stores each bond, only when it
starts. So the `bondfetch` init container writes the tree from the
`Secret`s before `bluetoothd` starts. When BlueZ changes the tree, the
operator copies the change back to the bond's `Secret`.

The operator serves DRA to the kubelet, and publishes each paired
device and the media bus. It copies each controller's input events
into a `uinput` device, so the device nodes of a claim stay the same while
the controller sleeps and reconnects. The operator writes the status
of each `Adapter`, `Peripheral`, and `PairingRequest`, and
`media-operator` reads each `Peripheral`. The `rescan` sidecar turns
the passive scan back on, so a bonded Low Energy remote can
reconnect. The `btmon` sidecar traces the link only when a trace is
requested.

## Design

The operator pairs devices, stores their bonds, and publishes them for
claims. A paired device stays published until a person deletes its
`Peripheral`. When the device disconnects or the radio is unplugged,
only the device's taints change. If the operator removed the device
instead, the kubelet would retry each claim on it with no limit.

The HID sessions of the controllers run inside `bluetoothd`. So when
BlueZ's name disappears from the bus, the operator exits, and it
doesn't publish controllers that it can't deliver.

The operator stores each bond in a `Secret` as an exact copy of
BlueZ's own files. It never parses them, so it keeps working if BlueZ
changes its file format. The link trace is off by default, because
`btmon` prints the link keys, and anyone who can read the pod's logs
would see them.

This operator runs no sound server. `audio-operator` publishes each
paired speaker as a sound output. It finds the media bus through the
device attribute and the CDI spec that this operator writes.

## Finding things

- `bluetoothd/rescan/main.go` describes the kernel faults that the
  `rescan` container works around, and when you can delete the
  container.
- buildroot builds the `bluetoothd` image from the tree in
  `liken/userland/`, with the fragment `bluetoothd.config`.
- Root plan 60 explains why the sound server is not in this pod.
