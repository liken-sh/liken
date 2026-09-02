---
title: Devices
weight: 30
toc: true
---

# Devices

This page describes what the operator publishes and what a claim on
it delivers: the devices, their attributes and taints, the two device
classes, and the objects of the pairing API. The operator is a
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
driver named `bluetooth.liken.sh`, and it publishes beside
[`liken`](https://liken.sh/docs/)'s own driver on the same node.

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
        - name: 04-4a-69-66-92-27-media
          attributes:
            address: {string: "04:4A:69:66:92:27"}
            kind: {string: mediaBus}
            sound.liken.sh/supportsSound: {bool: true}

The slice holds two kinds of device: one for each paired controller,
and one [media bus](#the-media-bus) for the adapter itself.

The controller list
follows the paired set, whether or not each controller is connected.
A paired controller that is switched off still publishes, so a pod
can claim it and start when somebody turns it on.
A controller leaves the slice only when it is unpaired, which is a
`kubectl delete peripheral`.

The media bus publishes as soon as `bluetoothd` names the adapter, so
the slice exists on a machine with a radio and nothing paired to it.
The whole slice is deleted only while no adapter has answered and
nothing is paired.

The device name is the controller's MAC in lowercase with dashes,
because a DRA device name must be a DNS label. The media bus takes
the adapter's own MAC in the same form, with a `-media` suffix. The
MAC is the only
identity on the machine that survives a reboot: the HID instance
suffix in sysfs counts up from zero each boot, and the `hci0:N`
handle changes on every reconnect, so a claim against either would
allocate different hardware after a reboot.

## The attributes

This section covers the paired controllers. [The media
bus](#the-media-bus) carries its own three attributes, listed in its
section.

A selector reads these as
`device.attributes["bluetooth.liken.sh"].<name>`.

Two attributes are on every controller this operator publishes, in
every state, the departed-adapter republish included:

| Attribute | Type | What it is |
|---|---|---|
| `address` | string | the controller's MAC, uppercase with colons: `A0:AB:51:33:B7:12` |
| `connected` | bool | whether `bluetoothd` holds a connection to it now |

Every other attribute is present only when BlueZ reports the fact.
The identity facts publish in two layers: the raw code, and the
names and flags unpacked from it, so a selector never does bit
arithmetic:

| Attribute | Type | What it is |
|---|---|---|
| `name` | string | the controller's alias in BlueZ, cut to 64 characters |
| `classOfDevice` | int | the raw 24-bit class word from the inquiry response |
| `appearance` | int | the LE appearance value; an LE-only device often reports this and no class word |
| `modalias` | string | the PnP vendor and product, as in `bluetooth:v000ApFFFFdFFFF`, cut to 64 characters |
| `icon` | string | BlueZ's own class-to-icon name, such as `audio-headphones` or `input-gaming` |
| `addressType` | string | `public` or `random` |
| `majorClass` | string | class bits 12 to 8 as a name: `audio-video`, `peripheral`, `phone`, and the other assigned majors |
| `minorClass` | string | class bits 7 to 2, read under the major: `headphones`, `gamepad`, `smartphone` |
| `servicePositioning`, `serviceNetworking`, `serviceRendering`, `serviceCapturing`, `serviceObjectTransfer`, `serviceAudio`, `serviceTelephony`, `serviceInformation` | bool | one flag per service bit the class word sets, bits 16 to 23 |

The profile flags come from the service UUIDs the device advertised
when it paired. Each one is `true` when the profile is advertised
and absent otherwise, and a UUID outside this vocabulary publishes
nothing:

| Attribute | The profile |
|---|---|
| `audioSink` | A2DP sink: the device plays audio |
| `audioSource` | A2DP source: the device sends audio |
| `avrcpTarget` | the device takes play, pause, and volume |
| `avrcpController` | the device sends play, pause, and volume |
| `handsfree` | HFP, the hands-free microphone profile |
| `headset` | HSP, the headset microphone profile |
| `input` | HID, classic or over GATT: the device is an input device |
| `battery` | the device reports a battery level |
| `serialPort` | raw RFCOMM serial |

The split between always and absent is a contract. The operator
omits an attribute it has no value for, rather than publishing it
empty. A selector's read of an absent attribute does not evaluate to
false; it fails, and the failure can abort the allocation instead of
skipping the device. So a selector on anything past `address` and
`connected` must guard the read:

    has(device.attributes["bluetooth.liken.sh"].name) &&
    device.attributes["bluetooth.liken.sh"].name.startsWith("DualSense")

A selector that reads only `address` or `connected` needs no guard,
because the `bluetooth-input` class already limits the candidates
to this driver's paired input devices, and both attributes are always
on them. Outside that class, the media bus carries `address` and no
`connected`, so `connected` takes a guard like any other attribute.

## The taints

Two taints go on a controller that cannot serve a claim, and they
answer two different questions:

| Taint | Effect | When |
|---|---|---|
| `bluetooth.liken.sh/disconnected` | `NoExecute` | `bluetoothd` reports the controller disconnected, or the adapter itself has departed |
| `bluetooth.liken.sh/no-input-node` | `NoSchedule` | the operator holds no virtual input node for the controller, which is a bond that has never connected since it was made |

The media bus takes one taint, and only when the adapter has
departed:

| Taint | Effect | When |
|---|---|---|
| `bluetooth.liken.sh/disconnected` | `NoSchedule` | the adapter has departed, so nothing answers on the bus |

The effect differs from the controllers' `NoExecute` on purpose. The
pod that holds the bus is the machine's one sound server, so an
eviction would end its other playback too, and that playback does not
need the radio. `NoSchedule` parks the next claim and leaves the
running holder alone.

**Tolerate `/disconnected` only.** The `NoExecute` taint evicts a
claim holder after its `tolerationSeconds`, so tolerating it sets how
long a radio may be silent before the pod ends. The `NoSchedule`
taint must stay untolerated, because it parks a claim on a controller
that has never connected as `Unschedulable`. Tolerate both and the
scheduler allocates a controller the operator has no node for,
`NodePrepareResources` fails, and the pod churns between
`ContainerCreating` and eviction until somebody switches the
controller on.

## The media bus

The media bus is one device per adapter: the claimable permission to
connect to this pod's `bluetoothd` over its private D-Bus. A
Bluetooth speaker creates no kernel device. Its audio exists only
while a sound server holds this bus and keeps a media endpoint
registered, and BlueZ advertises no A2DP until an endpoint registers.

The audio operator claims the bus through
`sound.liken.sh/supportsSound`, the attribute `liken` also stamps on
each sound card it publishes. That operator's class names the
attribute and no driver, so one claim collects every device on a node
that can serve a sound server. This operator ships no class for the
bus and runs no sound server itself.

| Attribute | Type | What it is |
|---|---|---|
| `address` | string | the adapter's own MAC, uppercase with colons |
| `kind` | string | `mediaBus` |
| `sound.liken.sh/supportsSound` | bool | always `true` |

`sound.liken.sh/supportsSound` is the one qualified name in this
driver's attributes. It lives in a domain neither driver owns, so a
selector reads it as
`device.attributes["sound.liken.sh"].supportsSound`, where every
other attribute here reads under `bluetooth.liken.sh`.

The bus never carries `input`, so the `bluetooth-input` class, which
guards on that attribute, never matches it.

The device is exclusive, which is `resource.k8s.io/v1`'s default: one
radio serves one sound server, because two media endpoints registered
on one `bluetoothd` have no contract over the streams.

A claim on the bus delivers a read-only mount of
`/var/run/bluetooth.liken.sh/dbus` at the same path inside the
container, and one environment variable that names the socket in it.
No device node, no privilege, and no other host path.

    DBUS_SYSTEM_BUS_ADDRESS=unix:path=/var/run/bluetooth.liken.sh/dbus/system_bus_socket

## The device classes

The operator takes two
[`DeviceClasses`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/),
and they split by owner:

| `DeviceClass` | Selector | Who claims it |
|---|---|---|
| `bluetooth-input` | `device.driver == "bluetooth.liken.sh"` and the `input` attribute, guarded | your workloads, one paired input device each |
| `bluetooth-adapter` | `device.driver == "liken.sh" && device.attributes["liken.sh"].driver == "btusb"` | the operator's own pod, for the raw radio |

`bluetooth-adapter` ships with the deploy base, because the
operator's own claim template names it and the pod cannot start
without it. `bluetooth-input` is yours to create, because a class a
workload claims through is cluster policy, and
[Install the operator](/docs/guides/install/) gives its YAML. It
selects the `input` attribute rather than the whole driver, because
the driver publishes more than input devices: a paired speaker
publishes as its bond record, no workload should hold one, and the
media bus belongs to the machine's sound server.

## The claim

A [`ResourceClaim`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/)
against `bluetooth-input` alone allocates any paired input device.
To name one, add a selector on its address:

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
gives the whole flow, with the pod that takes the claim. In a
`Deployment`, claim through a `ResourceClaimTemplate` rather than a
standing `ResourceClaim`, because a standing claim keeps its
allocation across an eviction.

## What a claim delivers

What a claim delivers depends on the device it allocated. Both kinds
arrive the same way, through the Container Device Interface (CDI) at
container creation, and neither delivers any privilege.

A claim on a controller delivers device nodes, and nothing else:
`/dev/input/event*`, one for each evdev node the controller
registers. No host mount, no environment variable. The container's
user must be able to open the nodes. A claim on the media bus
delivers the mount and the variable that [The media
bus](#the-media-bus) lists, and no device node.

The legacy `/dev/input/jsN` interface stays out. `liken`'s kernel may
not enable `CONFIG_INPUT_JOYDEV` at all, and joydev publishes a
DualSense's motion sensors as a wrong second `jsN` device.

The nodes a claim delivers are not the kernel's own nodes for the
controller. The operator creates one virtual input device for each
node the controller registers, with the kernel's `uinput` interface,
and moves the controller's events into it whenever the controller is
on the air. A virtual node keeps its number for as long as the
operator holds it open, so a controller that sleeps and returns as a
different `eventN` changes nothing a pod holds. A pod that tolerates
`/disconnected` and starts while the controller sleeps gets a node
that reads nothing until the next press.

The operator reads a controller's capabilities from its real node the
first time it connects, and stores them in that bond's `Secret`. On a
later start it creates the virtual devices from the stored snapshot,
before anything connects. So a controller has to connect once after
it is paired, and its claims stop parking from then on.

Events go one way. A gamepad's rumble is a write into the real node,
and the relay does not carry it back.

## Lifecycle

* **A disconnected controller is tainted, never deleted.** The device
  stays in the slice with both taints, and a return clears them.
  Deleting a device a claim still names would strand the claim: the
  kubelet retries `NodePrepareResources` against a device in no
  slice, with no bound on the retry.
* **A departed adapter taints everything.** When the radio itself is
  unplugged, the operator republishes the last paired set fully
  tainted and `connected: false`, so no allocation is stranded. The
  media bus republishes on the same pass with its own `NoSchedule`
  taint. The slice is deleted only while no adapter has answered and
  nothing is paired, which is the window before `bluetoothd` starts.
  Unpairing the last controller empties the paired set, not the
  slice: the media bus stays in it.
* **The operator's pod can restart under a live claim.** The prepared
  CDI files survive on the host, so a running consumer keeps the
  device node it was given. The replacement pod creates each virtual
  device again, from the snapshot in the bond's `Secret`, and the
  kernel numbers it from the free minors, which are the ones the old
  pod just released. A consumer whose node is not among them reads
  nothing until it restarts. The bus socket's directory is a host
  path for a related reason: a claim prepared against it names the
  same directory after the restart, where an emptyDir's host path is
  under `/var/lib/kubelet/pods/`, keyed by the pod's UID, and changes
  with the replacement pod.

## The pairing API

Three `CustomResourceDefinitions`, group `bluetooth.liken.sh/v1alpha1`,
each with its own reference page: an
[Adapter](/docs/reference/adapters/) is one radio, a
[Peripheral](/docs/reference/peripherals/) is one bonded device, and
a [PairingRequest](/docs/reference/pairingrequests/) is one pairing
window. A person creates a `PairingRequest`, edits a `Peripheral`'s
spec, and deletes a `Peripheral` to unpair; the operator creates and
reconciles everything else.

## Where the bonds are stored

One `Secret` for each bond, in the operator's namespace, named
`bluetooth-bond-<device>` after the controller's MAC. Each `Secret`
holds the two files BlueZ keeps for the bond, byte for byte, and a
`bluetooth.liken.sh/adapter` label naming the radio the bond is keyed
to. The bonds follow the radio: a dongle carried to another machine
takes its bonds with it, because the pod that claims it there lists
the same `Secrets`.

The keys are in the cluster datastore. Whether they are encrypted at
rest is a property of the cluster, not of this operator. Without
encryption at rest the keys are base64 in the datastore and its
backups.
