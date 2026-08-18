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

The slice holds one device for each **paired** controller, not for
each connected one. A paired controller that is switched off still
publishes, so a pod can claim it and start when somebody turns it on.
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

| Attribute | Type | Present | What it is |
|---|---|---|---|
| `address` | string | always | the controller's MAC, uppercase with colons: `A0:AB:51:33:B7:12` |
| `connected` | bool | always | whether bluetoothd holds a connection to it now |
| `name` | string | when the controller reports a name | the controller's alias in BlueZ, cut to 64 characters |

The Present column is a contract. `address` and `connected` are on
every device this operator publishes, in every state, the
departed-adapter republish included. `name` is not: the operator
omits an attribute it has no value for, rather than publishing it
empty, so a controller whose name is empty publishes no `name`. A
selector's read of an absent attribute does not evaluate to false;
it fails, and the failure can abort the allocation instead of
skipping the device. So a selector on `name` must guard the read:

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

**Tolerate `/disconnected` only.** The `NoExecute` taint is what
evicts a claim holder after its `tolerationSeconds`, so tolerating it
sets how long a radio may be silent before the pod ends. The
`NoSchedule` taint must stay untolerated: it is what parks a claim on
a switched-off controller as `Unschedulable`. Tolerate both and the
scheduler allocates a controller with no evdev node,
`NodePrepareResources` fails, and the pod churns between
`ContainerCreating` and eviction for as long as the controller stays
off.

## The device classes

The operator takes two
[`DeviceClasses`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/),
and you create both: a class is cluster policy, so the base ships
none, and [Install the operator](/docs/guides/install/) gives their
YAML. The names below are that guide's defaults, and
`bluetooth-controller` is the one your workloads claim:

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
the `NoExecute` taint is what ends it. A controller that reconnects
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
the repository holds it, carry the field-level reference.

| Kind | Scope | What it is |
|---|---|---|
| `Adapter` | Cluster | one radio, named for its address; the root that owns every `Pairing` keyed to it |
| `Pairing` | Cluster | one bond; deleting it is the unpair; it owns the `Secret` with the bond's keys |
| `PairingRequest` | Namespaced | one pairing window; `status.seen` lists what the radio saw, and writing `spec.device` approves one |

A `PairingRequest`'s spec takes four fields: `adapter` (required, the
`Adapter`'s name), `windowSeconds` (default 180, 15 to 900), `device`
(the approval; empty never pairs anything), and
`ttlSecondsAfterFinished` (default 86400). `PairingRequest` is
namespaced so RBAC can grant "may create `PairingRequests`" in one
namespace, with no exec into the operator's pod.

## Where the bonds live

One `Secret` for each bond, in the operator's namespace, named
`bluetooth-bond-<device>` after the controller's MAC. Each `Secret`
holds the two files BlueZ keeps for the bond, byte for byte, and a
`bluetooth.liken.sh/adapter` label naming the radio the bond is keyed
to. The bonds follow the radio: a dongle carried to another machine
takes its bonds with it, because the pod that claims it there lists
the same `Secrets`.

The keys sit in the cluster datastore. Whether they are encrypted at
rest is a property of the cluster, not of this operator. Without
encryption at rest the keys are base64 in the datastore and its
backups.
