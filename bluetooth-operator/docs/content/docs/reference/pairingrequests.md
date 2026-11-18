---
title: PairingRequest
weight: 20
toc: true
---

<!-- Generated from deploy/crds.yaml by crdref. Do not edit. -->

# `PairingRequest`

A `PairingRequest` opens one pairing window on one adapter. Create it
to open the window, watch `status.seen` for the controller that you
put in pairing mode, and approve that controller by writing its
address into `spec.device`. The operator pairs no device while
`spec.device` is empty. A `PairingRequest` is namespaced, so RBAC can
grant the right to pair in one namespace, and nobody needs a shell on
a node. A finished request reports `Paired` or `Expired`, and the
operator deletes it `spec.ttlSecondsAfterFinished` seconds later. The
[Peripheral](/docs/reference/peripherals/) that the request created
records the request's name in `status.bond.request`. [Pair a controller](/docs/guides/pair-a-controller/)
gives the steps.

One pairing window on one adapter. The operator starts a scan for devices when the window opens and stops it when the window closes, and `status.seen` lists the unpaired devices that BlueZ reports while the window is open.

## spec

The pairing that you ask the operator to do.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--adapter"></span>`adapter` | string | yes | The name of the `Adapter` to open the window on. The name is the radio's address in lowercase with dashes. Only the operator that runs that radio acts on the request. Pattern: `^([0-9a-f]{2}-){5}[0-9a-f]{2}$`. |
| <span id="spec--windowseconds"></span>`windowSeconds` | integer | no | How long the radio stays discoverable, pairable, and scanning, in seconds. The operator gives bluetoothd the same deadline, so the window closes by itself if the operator stops. Default: `180`. |
| <span id="spec--device"></span>`device` | string | no | The address of the device to pair with. Writing an address here approves that device. While the field is empty, the operator only scans and lists the devices it finds in `status.seen`. Write an address from `status.seen` here to pair that device. An empty field never pairs a device. Pattern: `^$\|^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$`. |
| <span id="spec--ttlsecondsafterfinished"></span>`ttlSecondsAfterFinished` | integer | no | How long the request stays after its phase becomes `Paired` or `Expired`, in seconds. After that time the operator deletes it. Default: `86400`. |

## status

What the operator observes during the pairing window.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--phase"></span>`phase` | string | no | `Open` while the pairing window runs. `Paired` when the approved device bonded. `Expired` when the window closed without an approved device. The operator does not retry from either end state. One of: `Open`, `Paired`, `Expired`. |
| <span id="status--windowclosesat"></span>`windowClosesAt` | string | no | When the pairing window closes if no device is approved. |
| <span id="status--seen"></span>`seen` | [\[\]object](#statusseen) | no | The devices that the radio found during this window and that have no bond in the cluster. The list has at most 16 entries, so that a place with many Bluetooth devices does not make the object grow without limit. |
| <span id="status--seentruncated"></span>`seenTruncated` | boolean | no | Whether `status.seen` left out a device because of its limit of 16 entries. |
| <span id="status--peripheral"></span>`peripheral` | string | no | The name of the `Peripheral` that this request created. |
| <span id="status--finishedat"></span>`finishedAt` | string | no | When the phase became `Paired` or `Expired`. The time in `spec.ttlSecondsAfterFinished` counts from this moment. |
| <span id="status--message"></span>`message` | string | no | Why the request has not paired the approved device, such as a pairing that bluetoothd refused. It is empty when there is nothing to report. |

### status.seen[]

The devices that the radio found during this window and that have no bond in the cluster. The list has at most 16 entries, so that a place with many Bluetooth devices does not make the object grow without limit.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusseen--address"></span>`address` | string | yes | The device's address. Write it to `spec.device` to approve the pairing. |
| <span id="statusseen--name"></span>`name` | string | no | The name that the device broadcasts, cut to 64 bytes, which is the longest value that a `ResourceSlice` attribute accepts. |
| <span id="statusseen--firstseen"></span>`firstSeen` | string | no | When the radio first found this device. |
