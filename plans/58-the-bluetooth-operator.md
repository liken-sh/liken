# The Bluetooth operator

Milestone 58 — Proposed. It would publish each paired Bluetooth
controller as its own DRA device, so a pod claims one controller by its
MAC address and receives that controller's evdev node and nothing else.
It is an instance of milestone 56's pattern: the operator claims the
Bluetooth adapter through an ordinary `liken.sh` claim, runs
bluetoothd, and publishes what bluetoothd holds under
`bluetooth.liken.sh`. The system image carries no BlueZ and no D-Bus.

## The problem

A Bluetooth controller is not a device that liken can publish. liken
walks the buses the kernel enumerates at boot and publishes what it
finds. The adapter is a USB device on that walk, and the controllers
are not devices on any bus until a person presses a pairing button.
They arrive over the radio, they appear on the HID bus, and they leave
again when a battery dies.

A workload that reads one controller today has two ways to get it, and
both are wrong.

* **Claim the adapter and read the raw radio.** The pod receives the
  adapter's usbfs node, and it must then implement pairing, link keys,
  and the HID transport itself. One pod does this for the whole
  machine, so a second workload gets nothing.
* **Run privileged with a hostPath on `/dev/input`.** The pod receives
  every input device the machine has, which on a machine with a console
  means the keyboard and the mouse as well. Nothing says which node is
  which controller, and the numbers change on every boot.

One fact governs the design: where the pairing state lives. bluetoothd
holds it, which is the paired set, the link keys, and the HID sessions.
It is not in sysfs and it is not on the Machine. So the layer that
publishes controllers must be the layer that runs bluetoothd, which is
the pattern milestone 56 states.

## What the pod runs and what it may do

The pod runs BlueZ's `bluetoothd`. bluetoothd owns pairing, the link
keys, and the HID sessions on top of them, and the lab proved the
ownership is not shareable: killing bluetoothd disconnects every
controller at once. The link keys persist on a volume, because a bond
that dies with the pod turns every restart into a re-pairing with a
person's hands on the controller.

bluetoothd speaks over the D-Bus system bus, so the pod runs a bus
daemon beside it. The bus is a socket in the pod's own filesystem, it
has one client and one service on it, and nothing outside the pod
reaches it. This is the whole reason the OS image needs no D-Bus: the
bus exists for one daemon, in the image that carries that daemon.

The operator also has to power the adapter. bluetoothd leaves an
adapter down unless its configuration says otherwise, so the image
ships a `main.conf` with `AutoEnable=true`, and a machine that reboots
comes back with its radio on and its bonds intact. Nothing in the
cluster has to press a button.

The privilege is `hostNetwork` plus `NET_ADMIN`, and nothing else.

* **`hostNetwork` is not optional.** `AF_BLUETOOTH` sockets exist only
  in the host network namespace. A socket call in a pod's own network
  namespace fails with `EAFNOSUPPORT`, which the lab tested. There is
  no device node or mount that changes this, because the Bluetooth
  stack's whole control surface is a socket family.
* **`NET_ADMIN` is what the kernel checks.** The management channel's
  privileged commands test for `CAP_NET_ADMIN`. `NET_RAW` looked like
  a companion requirement and proved unnecessary, so it stays off.

The two hostPath mounts every DRA driver takes come with that, and
milestone 56 lists them.

## The adapter claim

The operator acquires the adapter through a normal exclusive claim
against liken's driver, which is milestone 56's arbitration rule. Two
things in liken make the claim work, and liken justifies both on its
own terms, apart from this proposal.

* **The delivery walk stops at a Bluetooth subtree.** `InspectDelivery`
  in `hardware/delivery.go` walks the adapter's sysfs subtree. A
  connected controller hangs under the adapter's `hci0` node, so
  without a boundary the controller's input nodes join the adapter's
  delivery list. Then a pod that claims the adapter receives every
  controller's evdev node, and the delivery changes shape every time
  somebody turns a controller on. The walk stops at the Bluetooth
  subtree, so liken never publishes a controller node under any name.
  This is also what keeps the two drivers' delivery sets disjoint,
  which milestone 56 says nothing else enforces.
* **An idle btusb adapter publishes unconditionally.** The adapter must
  be claimable before any controller connects, because the operator has
  to hold it first. So a btusb adapter publishes as an exclusive device
  that delivers its usbfs node, whether or not anything is connected to
  it. It is the one device the inventory publishes with no delivered
  node of its own (`machine-operator/publishing.go`).

## One device for each controller

The operator publishes its own ResourceSlices under
`bluetooth.liken.sh`, with one device for each controller. The device
name is the peer MAC address in lowercase with dashes for the colons,
because a device name must be a DNS label. The unmodified MAC also
publishes as an attribute, so both forms are available: a claim that
names one controller spells the DNS label, and a selector or a person
reading the slice compares the address the way BlueZ, the kernel, and
the sticker on the controller all spell it.

The MAC is the only durable identity the machine has, and the lab
verified each part of that on hardware and in the kernel source.

* The HID device's `uevent` carries `HID_UNIQ=<peer mac>` and
  `HID_PHYS=<adapter mac>`. The child input devices expose the same
  peer address as `uniq`.
* The HID instance suffix in the device name, the `.0005` and the
  numbers after it, is a counter that restarts at every boot.
* The `hci0:N` connection handle changes on every reconnect.

So a name built from either number names a different controller after a
reboot, and a claim written against it allocates the wrong hardware. A
name built from the MAC survives reboots, reconnects, and a move to a
different adapter.

Discovery reads `/sys/bus/hid/devices` and keeps the entries whose bus
type is `0005`, which is `BUS_BLUETOOTH`. It must not filter by sysfs
ancestry to the adapter. BlueZ 5.73 and later run their input plugin in
uhid mode by default, and a uhid device's parent is
`/sys/devices/virtual/misc/uhid/`. It has no ancestry to `hci0` at all,
so an ancestry filter finds nothing on a current BlueZ.

## Paired, not connected

Because the operator uses BlueZ's D-Bus API, it reads the paired set
and not only the connected set. It publishes a paired controller that
is disconnected, the same as a connected one. A person can then create
a pod for a controller that is switched off, and the pod starts when
somebody turns the controller on. Milestone 56 gives the Kubernetes
behavior this rests on, and a switched-off controller is the case that
asked for it.

A controller that disconnects takes milestone 56's taint path, with
`tolerationSeconds` on the consumer's claim deciding how long a radio
may be silent before the pod ends. The taint is stricter than
bluetoothd's word: it applies when bluetoothd reports a disconnect,
and also when the controller registers no evdev node, because a
session can be up and mute. The lab met one: the ACL alive, BlueZ
answering `Connected: yes`, and no input device on the machine. The
taint tracks what a claim can deliver, not what bluetoothd believes.

## Events, not polling

The daemon listens for kernel uevents on a netlink socket bound to
group 1. An unprivileged process may receive that group, because the
kernel's uevent socket is created with `NL_CFG_F_NONROOT_RECV`. Two
traps around it are worth writing down, because both fail silently.

* **Do not bind group 2.** Group 2 is udev's own event group. On a
  machine with no udev the bind succeeds, the socket opens, and it
  delivers nothing forever, because `/run/udev/control` is absent and
  no udev is there to write to the group.
* **Do not run in a non-init user namespace.** The kernel delivers
  uevents to the initial user namespace only. A pod in its own user
  namespace receives an empty stream, with no error to read.

A removal uevent still carries `HID_UNIQ`, but the sysfs directory is
already gone when it arrives, so nothing can be read back. The daemon
keeps a map from `DEVPATH` to MAC address, populated at add time, and
resolves the removal through it.

A controller that reconnects in a loop is the flapping case milestone
56 debounces for.

## What a consumer receives

A claim delivers the controller's evdev nodes and nothing else. The
delivery is device nodes alone, which is what liken's own driver
delivers too, so the operator's CDI spec needs nothing that milestone
56 calls out as a wider form.

joydev stays out, for three reasons.

* The kernel's own joydev API documentation states that the interface
  is legacy and that evdev replaces it.
* liken's kernel build may not enable `CONFIG_INPUT_JOYDEV` at all, so
  a design that needs `/dev/input/jsN` needs a kernel change first.
* joydev exposes a DualSense's motion sensors as a second `jsN` device,
  which is wrong, and a kernel patch for it is pending. A consumer that
  reads evdev never meets the bug.

## What a drill must show

The drill runs against a real adapter and at least two real
controllers.

* **The Kustomization deploys it and deleting it removes it.** Before
  the apply, the machine publishes the devices it publishes today and
  the adapter stays idle. Apply the published base, and the operator's
  pod starts on the machine with the adapter, with no node selector
  written by hand. Delete it, and the published devices return to what
  they were.
* **Pairing.** Pair a controller through the operator's pod. The
  controller appears in a ResourceSlice under `bluetooth.liken.sh`,
  named by its MAC address, and the pairing survives a restart of the
  operator's pod.
* **A claim ahead of the connect.** Switch a paired controller off.
  Create a pod that claims it. The pod parks as Unschedulable. Turn the
  controller on, and the pod starts, with that controller's evdev node
  inside and no other input node.
* **Disconnect eviction with a toleration.** Switch the controller off
  while its pod runs. With a `tolerationSeconds` of thirty, a five
  second drop must not end the pod, and a drop past thirty seconds
  must end it.
* **Reconnect resumes.** After the eviction, turn the controller on
  again. The taint clears and the scheduler starts the consumer again,
  with no human step.
* **An operator restart while claims are live.** Delete the operator's
  pod while a consumer holds a controller. The prepared claims survive
  as CDI files on disk, so the running consumer keeps its device. The
  new pod re-registers with the kubelet, re-acquires the adapter claim,
  republishes its slices, and re-prepares the claims the kubelet asks
  it to.

## Open questions

* **Who owns the pairing UX.** The operator must run bluetoothd, so it
  can offer pairing. Whether it should is open: a pairing API is a
  privileged operation on a radio that reaches past the house walls,
  and the alternative is a one-time pairing done by a person with a
  short-lived pod. The leaning is a CRD in a later iteration: a person
  creates a pairing-request resource, the operator opens a pairing
  window, and the resource's status reports the result. The first
  release ships without it, and a person pairs by hand in the
  operator's pod.
* **Where the drill runs.** `liken-1` is the testbed and it has the
  hardware nearby. It is also where milestone 57's drill runs. Whether
  the two share the machine, or this drill moves to a machine with no
  display duties, is undecided.
