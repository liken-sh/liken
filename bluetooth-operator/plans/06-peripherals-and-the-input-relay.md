# 06, Peripherals and the input relay

Proposed.

This plan answers two open problems at once: BLE devices connect on
demand, and battery levels are not reported. The two share one cause.
The operator has no object for a bonded device, and the only node a
claim can deliver is the kernel's own evdev node, which appears and
vanishes with the radio link.

## The problem

**A Pairing is the bond, and a battery level is a fact of the device.**
The Pairing object holds the keys, when the bond was made, and which
request made it. It also reports `connected` and `deviceName`, which
are facts of the device. A charge level, a link state, and a name have
nowhere to go that names the device itself.

**A sleeping controller has no evdev node, and a claim delivers a node.**
A Bluetooth Low Energy remote drops its link after a short idle and
reconnects on the next press. BlueZ reconnects it with no help from this
operator. While it sleeps, the kernel has removed its evdev node. The
DRA prepare call fails when the controller registers no node, so the
`no-input-node` taint parks every claim on a sleeping controller. A
standing remote pod that has to be scheduled while the remote sleeps
stays Pending until somebody presses a button. The testbed showed one
Pending for hours with a working remote in the room.

**The node a running pod holds is fixed when the container starts.** The
container runtime creates a CDI device node once, from the major and
minor number the kernel gave the real node. A controller that
reconnects gets a new node, usually with the same number, and the pod's
node works again only because the kernel reused the number. The
reconcile pass rewrites the claim's CDI file when a node moves, and that
rewrite reaches only a container created later.

## The design

### The Peripheral

The Pairing becomes the Peripheral. It is the object for one bonded
device: cluster-scoped, one per bond, named by the device's address,
and created by the operator for every bond bluetoothd holds. The
operator writes the whole status. The spec keeps `alias` and `trusted`,
which reconcile into BlueZ's `Device1` properties as they do today.

The status has these parts:

* `address`, `name`, `icon`, `adapter`, and `node`. The identity facts
  the slice publishes, repeated here so a reader of the object never
  crosses to the slice. `name` is the name the device reports, and the
  printer column shows it, so `kubectl get peripherals` never shows a
  blank name for a device nobody renamed.
* `bond`, with `held`, `secret`, `pairedAt`, and `request`. The bond
  facts the Pairing held, as one block.
* `battery`, with `percentage`, `source`, and `charging`. The kernel
  is read first: a classic HID controller's reports never reach BlueZ,
  and its driver registers the battery in the power supply class
  under the HID device, with the level and the charging status. BlueZ's
  `org.bluez.Battery1` is the fallback, read in the same
  managed-objects read the pass already makes, which is where a Low
  Energy device's GATT Battery Service level arrives. The block is
  absent when neither reports a level.
* `conditions`, with `Connected`. The reason says why a device is not
  connected: `Asleep` for a bonded LE device between presses,
  `NotConnected` for a device that is switched off or out of range,
  and `NotBonded` when bluetoothd holds no object for it. A departed
  radio ends the pass before any status write, so the object keeps its
  last status until the radio returns.

Deleting a Peripheral stays the unpair API, with the same four-step
teardown. A PairingRequest that pairs a device names the Peripheral it
made in `status.peripheral`.

### What the slice keeps

The ResourceSlice answers what a claim can get and where. It keeps every
identity attribute, the `connected` attribute, and the `disconnected`
taint, because a consumer of a speaker may still want eviction when the
speaker goes off the air. The battery level never appears on the slice.
A level changes often and selects nothing, and every slice write wakes
every pod that waits on a claim.

The `no-input-node` taint stays with a narrower meaning: no relay exists
for this controller yet, so a claim would deliver nothing. That is true
only for a bond that has never connected since it was made.

### The input relay

For each bonded controller, the operator creates one virtual input
device with the kernel's `uinput` module and relays events into it from
the controller's real evdev node whenever that node exists. A uinput
device is an evdev node like any other, and its minor number is fixed
for as long as the operator holds it open. The claim delivers the
virtual node. A pod that starts while the controller sleeps gets a node
that reads nothing until the next press, and a pod that is running when
the controller sleeps keeps a node that works when it wakes.

The relay reads the controller's capabilities from the real node on the
first connect, which keys and which axes with their ranges, and stores
that snapshot in the bond's Secret under the key `evdev`. On a later
start, the operator creates the virtual device from the snapshot before
the controller connects. A controller has to connect once after this
ships before its claims stop parking.

Events flow one way. A gamepad's rumble is a write into the real node,
and the relay does not carry it back. Nothing uses it today.

The operator container needs three things it does not hold today:
`/dev/uinput`, the real evdev nodes as they come and go, and the adapter
claim that delivers both. liken's kernel builds `uinput` in, so every
liken machine has `/dev/uinput`. liken's delivery for a Bluetooth
adapter adds that node beside `/dev/uhid`, and adds the 32 legacy evdev
nodes by major and minor, so the container's nodes are in place before
the kernel registers a device at that number. The container runtime
creates a node from explicit numbers with no host node present, and
writes the matching device cgroup rule. The bound is 32 input devices
per machine.

### The consumers

The audio operator's Sink and Source name the Peripheral where they
named the Pairing, in `status.bluetooth.peripheral`.

The media operator watches Peripherals and reads two facts from the one
its Remote's claim allocated: the `Connected` condition and the battery
level. Both go on the Player status the idle screen draws, and the
Remote's status names its Peripheral. The standing remote pod stops
publishing presence, because with a stable node it cannot observe the
link, and it should not: the Peripheral is the record of the link.

## Considered and set aside

* **A battery field on the Pairing.** It would have worked, and it
  would have made the bond object describe the device. The name was the
  problem, and a Peripheral separate from the Pairing would have been
  two objects with one lifecycle to keep in step.
* **Publishing the level on the media bus.** The bus is the media
  operator's, above the hardware operators, and this operator publishes
  nothing on it. A cluster-scoped object is the Kubernetes-native home
  for a fact another operator reads.
* **An `available` state beside `connected`.** The open problem
  proposed one for a bonded device that reconnects on demand. With a
  stable node, a sleeping controller is a scheduling fact for nobody,
  and the `Connected` condition's reason says `Asleep`.
* **Delivering the whole `/dev/input` to a consumer.** A mount of the
  directory shows every input device on the machine, and the device
  cgroup cannot be scoped to one controller's future nodes: a CDI
  device node names one major and minor, with no wildcard.
* **A per-claim directory of nodes.** The same cgroup limit applies.
  The operator can create the nodes, and the container still cannot
  open a minor the runtime did not allow at start.

## Open after this plan

* A speaker's play, pause, and next keys arrive over AVRCP, and
  bluetoothd itself creates a uinput device for them. Whether the
  bluetoothd image is built with uinput support, and whether that node
  should publish as a controller, is a later plan.
* Force feedback across the relay.
* The 32-device bound on the evdev delivery.
* An operator restart closes every uinput fd and creates the virtual
  devices again, and the kernel numbers them from the free minors,
  which include the numbers the controllers' own nodes held. On the
  testbed the X6's two devices moved from `event11` and `event12` to
  `event8` and `event9`, and its pod found the DualSense's motion and
  touchpad devices behind its old nodes. The `node-moved` taint is the
  repair: a prepared claim whose CDI nodes are not the relay's current
  nodes takes a `NoExecute` taint no consumer tolerates, the pod is
  evicted, and the replacement is prepared with the current nodes. The
  cost is one pod restart per consumer on every operator restart.
* A controller whose capabilities change keeps its virtual device
  until the operator restarts, because rebuilding it takes the node
  away from a running container.
