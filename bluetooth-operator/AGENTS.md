# Working on bluetooth-operator

`bluetooth-operator` is a Go Kubernetes operator and a DRA driver with
the driver name `bluetooth.liken.sh`. `README.md` says what it is for
and describes its kinds.

## How it runs

The `bluetooth-operator` `DaemonSet` runs one pod on each node, except
a node with the label `bluetooth.liken.sh/bluetooth: none`. The pod
claims the radio that `liken` publishes for the `btusb` driver,
through the `DeviceClass` `bluetooth-adapter`. The pod runs in the
host's network namespace, because the kernel opens `AF_BLUETOOTH`
sockets only there.

- `bondfetch` is an init container. It reads the `Secret`s labelled
  with the adapter's address. From them it writes BlueZ's bond tree,
  the directory of files where BlueZ stores each bond, and the radio's
  identity key. It runs first because BlueZ reads these files only
  when it starts.
- `btmon` and `rescan` are native sidecars that start before
  `bluetoothd`. `btmon` traces the link when a trace is requested.
  `rescan` turns the passive scan back on, so that a bonded Low Energy
  remote can reconnect.
- `bluetoothd` is a native sidecar. It runs a private `dbus-daemon`
  and `bluetoothd`, and it puts the bus socket in a host path.
- `operator` is a client of that bus. It serves DRA to the kubelet,
  and it publishes each paired device and the media bus. For each
  controller, it copies the controller's input events into a `uinput`
  device. So the device nodes of a claim stay the same while the
  controller sleeps and reconnects. It writes the status of each
  `Adapter`, `Peripheral`, and `PairingRequest`. When BlueZ changes
  its bond tree, the operator copies the change back to the bond's
  `Secret`.

`audio-operator` claims the media bus and connects its WirePlumber to
the `bluetoothd` in this pod. `media-operator` reads each
`Peripheral`.

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

The reasons for these decisions are in `plans/`. Read the relevant
plan before you change how something works.

## Finding things

- The reference pages `docs/content/docs/reference/adapters.md`,
  `peripherals.md`, and `pairingrequests.md` are generated from
  `deploy/crds.yaml` and the text in `docs/crdref/`.
  `make -C docs generate` regenerates them, and `make -C docs skills`
  regenerates `skills/` from the guides. `make test` fails when either
  is out of date.
- `bluetoothd/rescan/main.go` describes the kernel faults that the
  `rescan` container works around, and when you can delete the
  container.
- buildroot builds the `bluetoothd` image from the tree in
  `liken/userland/`, with the fragment `bluetoothd.config`.
- Root plan 60 explains why the sound server is not in this pod.
