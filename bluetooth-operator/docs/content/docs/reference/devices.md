---
title: Devices
weight: 30
toc: true
---

# Devices

`bluetooth-operator` publishes each paired controller as a device that
a pod can claim, and [Pair a controller and give it to a
pod](/docs/guides/pair-a-controller/) uses them. The operator is a
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
driver named `bluetooth.liken.sh`. It publishes its devices on the
same node as [`liken`](https://liken.sh/docs/)'s own driver, `liken.sh`.

## The slice

The operator writes one
[`ResourceSlice`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-slice-v1/)
per node, named `<node>-bluetooth.liken.sh`, beside `liken`'s own
`<node>-liken.sh`:

    $ kubectl get resourceslice liken-1-bluetooth.liken.sh -o yaml
    spec:
      driver: bluetooth.liken.sh
      nodeName: liken-1
      devices:
        - name: a0-ab-51-33-b7-12
          attributes:
            address: {string: "A0:AB:51:33:B7:12"}
            connected: {bool: true}
            name: {string: "DualSense Wireless Controller"}
            classOfDevice: {int: 9480}
            majorClass: {string: peripheral}
            minorClass: {string: gamepad}
            addressType: {string: public}
            icon: {string: input-gaming}
            input: {bool: true}
            joystick: {bool: true}
            accelerometer: {bool: true}
            touchpad: {bool: true}
        - name: 04-4a-69-66-92-27-media
          attributes:
            address: {string: "04:4A:69:66:92:27"}
            kind: {string: mediaBus}
            sound.liken.sh/supportsSound: {bool: true}

The slice has a device for each paired controller and a
[media bus](#the-media-bus) device for the adapter.

Every paired controller is in the slice, connected or not. A
controller that is switched off stays in the slice, so a pod can claim
it and start when someone turns it on. A controller leaves the slice
only when you unpair it with `kubectl delete peripheral`.

The media bus appears as soon as `bluetoothd` reports the adapter, so
a machine with a radio and no paired controller still has a slice. The
operator deletes the slice only while no adapter has reported and
nothing is paired.

A device's name is the controller's MAC address in lowercase with
dashes, because a DRA device name must be a DNS label. The media bus
uses the adapter's MAC address in the same form, with a `-media`
suffix. The operator names devices by MAC address because it is the
only identity of a controller that survives a reboot. The HID instance
number in sysfs starts again from zero at each boot, and the `hci0:N`
connection handle changes at each reconnect, so a claim on either one
could allocate a different device after a reboot.

## The attributes

The attributes below belong to the controllers. [The media
bus](#the-media-bus) has its own.

A selector reads an attribute as
`device.attributes["bluetooth.liken.sh"].<name>`.

Every controller always has these attributes, even after the adapter
is unplugged:

| Attribute | Type | What it is |
|---|---|---|
| `address` | string | the controller's MAC, uppercase with colons: `A0:AB:51:33:B7:12` |
| `connected` | bool | whether `bluetoothd` is connected to it now |

The operator sets each of the other attributes only when BlueZ reports
a value. The controller's class appears both as the raw code and as
names and flags decoded from it, so a selector needs no bit
arithmetic:

| Attribute | Type | What it is |
|---|---|---|
| `name` | string | the controller's alias in BlueZ, cut to 64 bytes |
| `classOfDevice` | int | the raw 24-bit class word from the inquiry response |
| `appearance` | int | the LE appearance value. A device that supports only LE often reports this and no class word |
| `modalias` | string | the PnP vendor and product, as in `bluetooth:v000ApFFFFdFFFF`, cut to 64 bytes |
| `icon` | string | the icon name that BlueZ derives from the class, such as `audio-headphones` or `input-gaming` |
| `addressType` | string | `public` or `random` |
| `majorClass` | string | class bits 12 to 8 as a name: `audio-video`, `peripheral`, `phone`, and the other assigned majors |
| `minorClass` | string | class bits 7 to 2, read under the major: `headphones`, `gamepad`, `smartphone` |
| `servicePositioning`, `serviceNetworking`, `serviceRendering`, `serviceCapturing`, `serviceObjectTransfer`, `serviceAudio`, `serviceTelephony`, `serviceInformation` | bool | one flag per service bit the class word sets, bits 16 to 23 |

The profile flags come from the service UUIDs that the device
advertised when it paired. A flag is `true` when the device advertises
the profile, and absent otherwise. The operator ignores any UUID that
is not in this table:

| Attribute | The profile |
|---|---|
| `audioSink` | A2DP sink: the device plays audio |
| `audioSource` | A2DP source: the device sends audio |
| `avrcpTarget` | the device accepts play, pause, and volume commands |
| `avrcpController` | the device sends play, pause, and volume commands |
| `handsfree` | HFP, the hands-free microphone profile |
| `headset` | HSP, the headset microphone profile |
| `input` | HID, classic or over GATT: the device is an input device |
| `battery` | the device reports a battery level |
| `serialPort` | raw RFCOMM serial |

The input classes come from the kernel's evdev nodes for the
controller, not from Bluetooth. They are `udev`'s `ID_INPUT_*`
properties in lower case: `key`, `keyboard`, `mouse`, `pointingstick`,
`touchpad`, `touchscreen`, `tablet`, `tablet_pad`, `joystick`,
`accelerometer`, and `switch`. A class is `true` when any of the
controller's nodes has it, and absent otherwise. [The `inputs`
parameter](#the-inputs-parameter) has a table of what puts a node in
each class.

The operator reads a controller's capabilities the first time it
connects and stores them in the bond's `Secret`, so the classes stay in
the slice while the controller sleeps. A bond that has never connected
has `input` and no class, because the operator has not read its nodes
yet.

A CEL selector that reads a missing attribute does not evaluate to
false. The read fails, and the failure can stop the allocation instead
of skipping the device. So a selector that reads any attribute other
than `address` and `connected` must test for it first:

    has(device.attributes["bluetooth.liken.sh"].name) &&
    device.attributes["bluetooth.liken.sh"].name.startsWith("DualSense")

Through the `bluetooth-input` class, a selector can read `address` or
`connected` with no test, because that class matches only paired input
devices of this driver, and each of them has both attributes. Outside
that class a selector must test `connected` too, because the media bus
has `address` and no `connected`.

## The taints

A controller can have the taints in this table. `disconnected` and
`no-input-node` mean that the controller cannot serve a claim now.
`node-moved` means that a claim the controller already serves has the
wrong device nodes:

| Taint | Effect | When |
|---|---|---|
| `bluetooth.liken.sh/disconnected` | `NoExecute` | `bluetoothd` reports the controller disconnected, or the adapter is unplugged |
| `bluetooth.liken.sh/no-input-node` | `NoSchedule` | the operator has no virtual input node for the controller. This is true of a bond that has not connected since it was created |
| `bluetooth.liken.sh/node-moved` | `NoExecute` | a prepared claim has device nodes that differ from the nodes the operator would give it now. This can happen after the operator restarts |

The media bus has a taint only when the adapter is unplugged:

| Taint | Effect | When |
|---|---|---|
| `bluetooth.liken.sh/disconnected` | `NoSchedule` | the adapter is unplugged |

On the media bus the effect is `NoSchedule` and not `NoExecute`,
because the pod that holds the bus is the machine's sound server. An
eviction would also stop the sound that it plays through the machine's
own sound hardware, which does not need the radio. `NoSchedule` keeps
a new claim from allocating the bus, and leaves the running pod alone.

**Tolerate `/disconnected` only.** The `NoExecute` taint evicts the pod
that holds the claim after `tolerationSeconds`, so the toleration sets
how long the controller can be disconnected before the pod is evicted.
Do not tolerate `/no-input-node`. Without that toleration, a claim on a
controller that has never connected waits until it can be scheduled.
With it, the scheduler allocates a controller that the operator has no
node for, `NodePrepareResources` fails, and the pod goes between
`ContainerCreating` and eviction until someone switches the controller
on.

Do not tolerate `/node-moved` either. The pod has device nodes that now
belong to another controller, and the eviction fixes that: the kubelet
unprepares the claim, and the replacement container gets the current
nodes.

## The media bus

The media bus is a device for each adapter. A claim on it lets a pod
connect to the operator pod's `bluetoothd` over a private D-Bus. A
Bluetooth speaker creates no kernel device. Its audio exists only
while a sound server is connected to this bus and has registered a
media endpoint, because BlueZ does not advertise A2DP until an endpoint
registers.

`audio-operator` claims the bus through the attribute
`sound.liken.sh/supportsSound`, which `liken` also sets on each sound
card it publishes. The `DeviceClass` of `audio-operator` selects on
that attribute and names no driver, so one claim can match any device
on a node that a sound server can use. `bluetooth-operator` has no
device class for the bus and runs no sound server.

| Attribute | Type | What it is |
|---|---|---|
| `address` | string | the adapter's own MAC, uppercase with colons |
| `kind` | string | `mediaBus` |
| `sound.liken.sh/supportsSound` | bool | always `true` |

`sound.liken.sh/supportsSound` is in its own domain, so a selector
reads it as `device.attributes["sound.liken.sh"].supportsSound`. Every
other attribute of this driver is under `bluetooth.liken.sh`.

The bus never has the `input` attribute, so the `bluetooth-input`
class never matches it.

The device is exclusive, which is the default in `resource.k8s.io/v1`.
A radio serves one sound server, because the behavior of the streams
is not defined when two media endpoints register with one `bluetoothd`.

A claim on the bus gives the container a read-only mount of
`/var/run/bluetooth.liken.sh/dbus` at the same path, and this
environment variable, which names the socket in that directory:

    DBUS_SYSTEM_BUS_ADDRESS=unix:path=/var/run/bluetooth.liken.sh/dbus/system_bus_socket

It gives the container no device node, no privilege, and no other host
path.

## The device classes

Your workloads and the operator's own pod claim devices through these
[`DeviceClasses`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/):

| `DeviceClass` | Selector | Who claims it |
|---|---|---|
| `bluetooth-input` | `device.driver == "bluetooth.liken.sh"` and the `input` attribute, guarded | your workloads, for one paired input device each |
| `bluetooth-adapter` | `device.driver == "liken.sh" && has(device.attributes["liken.sh"].driver) && device.attributes["liken.sh"].driver == "btusb"` | the operator's own pod, for the device that `liken` publishes for the radio's `btusb` driver |

`bluetooth-adapter` is in the deploy base, because the operator's own
claim template uses it and the pod cannot start without it. You create
`bluetooth-input` yourself, because which devices your workloads may
claim is a policy of your cluster. [Install the
operator](/docs/guides/install/) has its YAML. A class of your own can
also set a default [`inputs` block](#the-inputs-parameter). The class
selects on the `input` attribute and not on the whole driver, because
the driver also publishes devices that no workload should claim: a
paired speaker, which has no `input` attribute, and the media bus,
which belongs to the machine's sound server.

## The claim

A [`ResourceClaim`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/)
that names only `bluetooth-input` can allocate any paired input
device. To choose one controller, add a selector on its address:

    spec:
      devices:
        requests:
          - name: controller
            exactly:
              deviceClassName: bluetooth-input
              selectors:
                - cel:
                    expression: |
                      device.attributes["bluetooth.liken.sh"].address == "A0:AB:51:33:B7:12"
              tolerations:
                - key: bluetooth.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30

[Pair a controller and give it to a pod](/docs/guides/pair-a-controller/)
covers the whole procedure, including the pod that uses the claim. In
a `Deployment`, use a `ResourceClaimTemplate` and not a separate
`ResourceClaim`, because a separate claim keeps its allocation after
a pod is evicted.

## The inputs parameter

A claim's `inputs` parameter chooses which input classes reach the
container. Most consumers want only some of them. A DualSense sends a
steady stream of motion events from its accelerometer, and a pod that
reads only the buttons would have to discard them all. When a claim
sets `inputs`, the operator does not pass the events of the other
classes to the container.

    spec:
      devices:
        requests:
          - name: controller
            exactly:
              deviceClassName: bluetooth-input
        config:
          - requests: [controller]
            opaque:
              driver: bluetooth.liken.sh
              parameters:
                inputs: [joystick]

A claim with no configuration block, or with a block that has no
`inputs` key, receives every class. The operator rejects an empty list
or a name that is not in the table below when it prepares the claim.
The pod then stays in `ContainerCreating`, and its events show the
error, which lists the valid names.

The operator assigns the classes with a port of the rules in
`systemd`'s `input_id` builtin, so `udevadm info` on any input device
prints the same names:

| Class | The `udev` property | What qualifies |
|---|---|---|
| `key` | `ID_INPUT_KEY` | any `KEY_*` code below `BTN_MISC`, or in the two `KEY_*` blocks above it, or a node whose only capability is a scroll wheel |
| `keyboard` | `ID_INPUT_KEYBOARD` | every one of the first 31 key codes, which is escape, the numbers, and Q to D |
| `mouse` | `ID_INPUT_MOUSE` | a `BTN_MOUSE` button with `REL_X` and `REL_Y`, or with no absolute axes |
| `pointingstick` | `ID_INPUT_POINTINGSTICK` | the `INPUT_PROP_POINTING_STICK` property, or a mouse on the i2c bus |
| `touchpad` | `ID_INPUT_TOUCHPAD` | `BTN_TOOL_FINGER` and no pen, on a node that is not `INPUT_PROP_DIRECT` |
| `touchscreen` | `ID_INPUT_TOUCHSCREEN` | absolute or multi-touch coordinates with `BTN_TOUCH`, or the `INPUT_PROP_DIRECT` property |
| `tablet` | `ID_INPUT_TABLET` | `BTN_STYLUS` or `BTN_TOOL_PEN` with absolute coordinates |
| `tablet_pad` | `ID_INPUT_TABLET_PAD` | `BTN_0` and `BTN_1` on a tablet, or with a wheel and no relative coordinates |
| `joystick` | `ID_INPUT_JOYSTICK` | a button in the `BTN_JOYSTICK` range, a trigger or D-pad button, or an axis from `ABS_RX` to `ABS_PRESSURE` |
| `accelerometer` | `ID_INPUT_ACCELEROMETER` | the `INPUT_PROP_ACCELEROMETER` property, or three absolute axes and no keys |
| `switch` | `ID_INPUT_SWITCH` | any `EV_SW` code |

A node can be in several classes, just as `udev` can set several
properties on one node. A remote with a gyroscopic cursor is both `key`
and `mouse`. The operator does not read a node that no prepared claim
asks for. When a node is in a class that the claim asked for and in one
that it did not, the operator passes on only the event types of the
classes that the claim asked for. So a claim with `inputs: [key]` on
that remote receives its keys and none of its pointer motion.

When two claims use one controller, each receives the classes it asked
for. The operator passes on the union of the classes that the prepared
claims ask for, and passes on fewer when a claim ends.

To set a default, put `inputs` in a `DeviceClass` of your own. Every
claim through that class receives it, and a claim's own `inputs` block
overrides the class's.

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: gamepad
    spec:
      selectors:
        - cel:
            expression: |
              device.driver == "bluetooth.liken.sh" &&
              has(device.attributes["bluetooth.liken.sh"].joystick)
      config:
        - opaque:
            driver: bluetooth.liken.sh
            parameters:
              inputs: [joystick]

The selector chooses which devices the claim can allocate, and
`inputs` chooses which events reach the container after the device is
allocated.

The `bluetooth-input` class in [Install the
operator](/docs/guides/install/) has no `inputs` block, so a claim
through it receives every class. A class can also set [an `axes`
block](#the-axes-parameter).

## The axes parameter

An analog stick at rest reports small changes of position from noise.
A DualSense's right stick does this all the time on `ABS_RX` and
`ABS_RY`, so its node sends a steady stream of events, and every
program that reads the node must process each one. The kernel filters
a noisy axis with the axis's `fuzz` value, and the `axes` parameter
sets that value for a claim:

    spec:
      devices:
        requests:
          - name: controller
            exactly:
              deviceClassName: gamepad
        config:
          - requests: [controller]
            opaque:
              driver: bluetooth.liken.sh
              parameters:
                inputs: [joystick]
                axes:
                  ABS_RX: {fuzz: 4}
                  ABS_RY: {fuzz: 4}

Before any program reads an event, the kernel's input core drops a
change of position that is smaller than half the axis's `fuzz`, and
smooths a change that is smaller than `fuzz`. It reports any position
within `flat` of the centre as the centre.

On other Linux machines, `systemd`'s hwdb sets the same two values for
known devices, through the `EVDEV_ABS_` entries in `60-evdev.hwdb` and
the same `EVIOCSABS` ioctl. The operator has no hardware database, so
the claim sets the values.

The keys are the kernel's `ABS_*` code names from
`linux/input-event-codes.h`. Each key takes `fuzz`, `flat`, or both,
and no other field:

| Field | What it is |
|---|---|
| `fuzz` | the size of the noise to filter out, in units of the axis's own range |
| `flat` | the size of the dead zone around the centre, in the same units |

When it prepares the claim, the operator rejects a code name that is
not in the kernel's list, any other field, and a negative value. The
pod stays in `ContainerCreating`, and the error lists what the
operator accepts. The operator also rejects `ABS_MT_SLOT`, because the
kernel does not let a program change the number of contacts that a
device reports.

For each axis, the operator uses the largest `fuzz` and the largest
`flat` among the prepared claims on the controller, so each claim gets
at least the filtering it asked for. When no remaining claim sets
values for an axis, the axis goes back to the values that the device
reported when it first connected. The operator stores those values in
the bond's `Secret`.

The operator writes the values to the controller's kernel node when it
prepares the claim, and again each time the controller reconnects,
because a reconnect creates a new kernel device with the device's own
values.

## What a claim delivers

The kubelet passes what a claim grants to the container runtime
through the Container Device Interface (CDI) when it creates the
container. Neither kind of claim gives the container extra privilege.

A claim on a controller gives the container device nodes and nothing
else: a `/dev/input/event*` for each evdev node of the controller. The
container's user must have permission to open the nodes. The container
gets every node whatever the claim's `inputs` are, and [the `inputs`
parameter](#the-inputs-parameter) chooses which events arrive on each
node. A claim on the media bus gives the mount and the environment
variable that [The media bus](#the-media-bus) describes, and no
device node.

The operator does not give a container the legacy `/dev/input/jsN`
nodes of joydev, because the kernel documents joydev as legacy, and
joydev publishes a DualSense's motion sensors as a second joystick.

The device nodes in the container are not the kernel's nodes for the
controller. For each of the controller's nodes, the operator creates a
virtual input device through the kernel's `uinput` interface, and
copies the controller's events into it while the controller is
connected. A virtual node keeps its number for as long as the operator
keeps it open. So when a controller sleeps and comes back as a
different `eventN`, the nodes in the pod do not change. A pod that
tolerates `/disconnected` and starts while the controller sleeps gets
a node that reads nothing until the next press.

The first time a controller connects, the operator reads its
capabilities from its kernel node and stores them in the bond's
`Secret`. When the operator starts later, it creates the virtual
devices from the stored capabilities before any controller connects.
So a controller must connect once after you pair it. Until it does,
the controller has the `no-input-node` taint and its claims cannot be
scheduled.

Events go only from the controller to the container. A gamepad's
rumble is a write to the node, and the operator does not send it back
to the controller.

Some Bluetooth LE remotes send no key for the press that wakes them.
An X6 air-mouse remote ends its own link when it sleeps, and sends the
press that wakes it, so a claim receives that press. A T6 remote lets
its link time out, and sends nothing for the press that wakes it. So
when a remote connects again after a sleep, the operator writes one
press and one release of `KEY_UNKNOWN` (code 240) into its virtual
node, before the remote's own events. A consumer that has no binding
for `KEY_UNKNOWN` treats it as a press that does nothing, and a
sleeping screen wakes on it. The operator writes the press only when
all of these are true:

* The `Peripheral`'s `Connected` condition had the reason `Asleep`,
  which means that the link timed out on a device that sleeps between
  sessions.
* The link was down for at least 10 seconds. A remote that lost its
  link while it was awake connects again within seconds, and gets no
  press.
* The node reports a keyboard key, which is a code below `BTN_MISC`.
  A gamepad's node gets no press.
* A claim on the controller receives `EV_KEY` events from that node.

The operator writes one press for each sleep, on the first node that
meets these conditions. Every virtual node that has a keyboard key
declares `KEY_UNKNOWN`, even when the controller's own node does not.
The stored capabilities, the input classes, and the slice do not list
it.

## Lifecycle

* **A disconnected controller keeps its device.** The device stays in
  the slice with the `disconnected` taint, and the operator removes the
  taint when the controller connects again. The operator keeps the
  device because, if it deleted a device that a claim still uses, the
  kubelet would retry `NodePrepareResources` for a device that is in
  no slice, with no limit.
* **When the adapter is unplugged, every device gets a taint.** The
  operator publishes the last known set of paired controllers again,
  each with `connected: false` and the `disconnected` taint, so every
  allocation still has its device. In the same write it publishes the
  media bus with its `NoSchedule` taint. When you unpair the last
  controller, the slice keeps the media bus.
* **The operator's pod can restart while a claim is in use.** The CDI
  files are in `/var/run/cdi` on the host, so a running consumer keeps
  its device nodes. The new operator pod creates each virtual device
  again from the capabilities stored in the bond's `Secret`. The
  kernel gives the new devices free numbers, which can now include the
  numbers that the controllers' own nodes use. A consumer whose nodes
  differ from the nodes that the operator would give it now gets the
  `/node-moved` taint, and its eviction fixes it. The directory of the
  bus socket is a host path for the same reason: a claim prepared with
  that path still points at the right directory after the restart. An
  `emptyDir` would not work, because its host path is under
  `/var/lib/kubelet/pods/` and contains the pod's UID, so it changes
  when the pod is replaced.

## The pairing API

The API group `bluetooth.liken.sh/v1alpha1` has three
`CustomResourceDefinitions`, each with a reference page. An
[Adapter](/docs/reference/adapters/) is one radio. A
[Peripheral](/docs/reference/peripherals/) is one bonded device. A
[PairingRequest](/docs/reference/pairingrequests/) is one pairing
window. You create a `PairingRequest`, edit the spec of a
`Peripheral`, and delete a `Peripheral` to unpair it. The operator
creates and updates every other object.

## Where the bonds are stored

The operator stores each bond in its own `Secret` in the operator's
namespace, named `bluetooth-bond-<device>` after the controller's MAC
address. The `Secret` has an exact copy of the two files that BlueZ
keeps for the bond, and a `bluetooth.liken.sh/adapter` label with the
name of the radio that the bond belongs to. A bond belongs to a radio,
not to a machine. When you move a USB radio to another machine, its
bonds move with it, because the operator pod that claims the radio
there reads the same `Secrets`.

The pairing keys are in the cluster's datastore. Encryption at rest is
a setting of the cluster, not of this operator. Without it, the
datastore and its backups have the keys in base64.
