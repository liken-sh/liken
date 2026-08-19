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

The slice holds one device for each paired controller. The list
follows the paired set, whether or not each controller is connected.
A paired controller that is switched off still publishes, so a pod
can claim it and start when somebody turns it on.
A device leaves the slice only when it is unpaired, which is a
`kubectl delete pairing`.

The device name is the controller's MAC in lowercase with dashes,
because a DRA device name must be a DNS label. The MAC is the only
identity on the machine that survives a reboot: the HID instance
suffix in sysfs counts up from zero each boot, and the `hci0:N`
handle changes on every reconnect, so a claim against either would
allocate different hardware after a reboot.

## The attributes

A selector reads these as
`device.attributes["bluetooth.liken.sh"].<name>`.

Two attributes are on every device this operator publishes, in every
state, the departed-adapter republish included:

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
because the `bluetooth-controller` class already limits the
candidates to this driver's devices, and both attributes are always
on them.

## The taints

Two taints go on a device that cannot serve a claim, and they answer
two different questions:

| Taint | Effect | When |
|---|---|---|
| `bluetooth.liken.sh/disconnected` | `NoExecute` | bluetoothd reports the controller disconnected, or it registers no evdev node, or the adapter itself has departed |
| `bluetooth.liken.sh/no-input-node` | `NoSchedule` | the controller registers no evdev node, or the adapter itself has departed |

**Tolerate `/disconnected` only.** The `NoExecute` taint evicts a
claim holder after its `tolerationSeconds`, so tolerating it sets how
long a radio may be silent before the pod ends. The `NoSchedule`
taint must stay untolerated, because it parks a claim on a
switched-off controller as `Unschedulable`. Tolerate both and the
scheduler allocates a controller with no evdev node,
`NodePrepareResources` fails, and the pod churns between
`ContainerCreating` and eviction for as long as the controller stays
off.

## The device classes

The operator takes two
[`DeviceClasses`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/),
and the deploy base ships both, because their selectors name only
this driver's own vocabulary and a release must be able to change a
selector. [Install the operator](/docs/guides/install/) shows their
YAML. `bluetooth-controller` is the one your workloads claim:

| `DeviceClass` | Selector | Who claims it |
|---|---|---|
| `bluetooth-controller` | `device.driver == "bluetooth.liken.sh"` | your workloads, one paired controller each |
| `bluetooth-adapter` | `device.driver == "liken.sh" && device.attributes["liken.sh"].driver == "btusb"` | the operator's own pod, for the raw radio |

## The claim

A [`ResourceClaim`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/)
against `bluetooth-controller` alone allocates any paired controller.
To name one, add a selector on its address:

    spec:
      devices:
        requests:
          - name: controller
            exactly:
              deviceClassName: bluetooth-controller
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

Device nodes, and nothing else: `/dev/input/event*` for the one
controller the claim allocated, injected through the Container Device
Interface (CDI) at container creation. No privilege, no host mount,
no environment variable. The container's user must be able to open
the nodes.

The legacy `/dev/input/jsN` interface stays out. `liken`'s kernel may
not enable `CONFIG_INPUT_JOYDEV` at all, and joydev publishes a
DualSense's motion sensors as a wrong second `jsN` device.

A running pod's device set never changes. The runtime injects the
nodes when it creates the container, so the pod is one session, and
the `NoExecute` taint ends it. A controller that reconnects
usually returns as a different `eventN`, and the operator rewrites
the claim's CDI file so the next pod receives the node that exists
now.

## Lifecycle

* **A disconnected controller is tainted, never deleted.** The device
  stays in the slice with both taints, and a return clears them.
  Deleting a device a claim still names would strand the claim: the
  kubelet retries `NodePrepareResources` against a device in no
  slice, with no bound on the retry.
* **A departed adapter taints everything.** When the radio itself is
  unplugged, the operator republishes the last paired set fully
  tainted and `connected: false`, so no allocation is stranded. The
  slice is deleted only when an adapter is present and its paired set
  is empty, which means every controller was unpaired.
* **The operator's pod can restart under a live claim.** The prepared
  CDI files survive on the host, so a running consumer keeps its
  device across the restart.

## The pairing API

Three `CustomResourceDefinitions`, group `bluetooth.liken.sh/v1alpha1`.
The operator creates and reconciles all of them; a person creates a
`PairingRequest`, edits a `Pairing`'s spec, and deletes a `Pairing` to
unpair. The schema descriptions in
[`deploy/crds.yaml`](/deploy/crds.yaml), which this site serves as
the repository holds it, are the field-level reference.

| Kind | Scope | What it is |
|---|---|---|
| `Adapter` | Cluster | one radio, named for its address; the root that owns every `Pairing` keyed to it |
| `Pairing` | Cluster | one bond; deleting it is the unpair; it owns the `Secret` with the bond's keys |
| `PairingRequest` | Namespaced | one pairing window; `status.seen` lists what the radio observed, and writing `spec.device` approves one |

A `PairingRequest`'s spec takes four fields: `adapter` (required, the
`Adapter`'s name), `windowSeconds` (default 180, 15 to 900), `device`
(the approval; empty never pairs anything), and
`ttlSecondsAfterFinished` (default 86400). `PairingRequest` is
namespaced so RBAC can grant "may create `PairingRequests`" in one
namespace, with no exec into the operator's pod.

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
