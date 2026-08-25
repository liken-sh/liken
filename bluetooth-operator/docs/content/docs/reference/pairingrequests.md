---
title: PairingRequest
weight: 20
toc: true
---

<!-- Generated from deploy/crds.yaml by crdref. Do not edit. -->

A `PairingRequest` is one pairing window on one adapter. Create it
to open the window, watch `status.seen` for the controller you put
in pairing mode, and approve that controller by writing its address
into `spec.device`. An empty `spec.device` never pairs anything.
The resource is namespaced, so RBAC can grant the right to pair in
one namespace without a shell on any node. A finished request
reports `Paired` or `Expired` and is collected after its TTL, and
the [Pairing](/docs/reference/pairings/) it produced records the
request's name. [Pair a controller](/docs/guides/pair-a-controller/)
gives the steps.

One pairing window on one adapter. The scan and the window are one radio session, so an address in status.seen is one the radio observed in this same session.

## spec

What a person asks the operator to do.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--adapter"></span>`adapter` | string | yes | The name of the Adapter to open the window on, which is that radio's address in lowercase with dashes. Only the operator holding that radio acts on the request. Pattern: `^([0-9a-f]{2}-){5}[0-9a-f]{2}$`. |
| <span id="spec--windowseconds"></span>`windowSeconds` | integer | no | How long the radio stays discoverable, pairable, and scanning. The operator gives bluetoothd the same deadline, so the window closes on its own if the operator stops. Default: `180`. |
| <span id="spec--device"></span>`device` | string | no | The address to pair with, which is the approval. Leave it empty to scan and report, and write an address from status.seen into it to pair that one device. An empty value never pairs anything. Pattern: `^$\|^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$`. |
| <span id="spec--ttlsecondsafterfinished"></span>`ttlSecondsAfterFinished` | integer | no | How long the request stays after it reaches Paired or Expired. The Pairing records the request that produced it, so that record outlasts the deletion. Default: `86400`. |

## status

What the operator observes during the window.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--phase"></span>`phase` | string | no | Open while the window runs. Paired when the approved device bonded, and Expired when the window closed with no approval. Neither end state retries. One of: `Open`, `Paired`, `Expired`. |
| <span id="status--windowclosesat"></span>`windowClosesAt` | string | no | When the window closes if nobody approves a device. |
| <span id="status--seen"></span>`seen` | [\[\]object](#statusseen) | no | The devices the radio observed during this window that the cluster holds no bond with. The list is capped at 16 entries, because it is written from radio observations, and a busy room would otherwise grow the object without limit. |
| <span id="status--pairing"></span>`pairing` | string | no | The name of the Pairing this request produced. |
| <span id="status--finishedat"></span>`finishedAt` | string | no | When the request reached Paired or Expired, which is what ttlSecondsAfterFinished counts from. |
| <span id="status--message"></span>`message` | string | no | Why the request has not done what it was asked to do, such as a pairing bluetoothd refused. It is empty when there is nothing to report. |

### status.seen[]

The devices the radio observed during this window that the cluster holds no bond with. The list is capped at 16 entries, because it is written from radio observations, and a busy room would otherwise grow the object without limit.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusseen--address"></span>`address` | string | yes | The device's address. Write this into spec.device to approve the pairing. |
| <span id="statusseen--name"></span>`name` | string | no | The name the device broadcasts, cut to the same 64 bytes a ResourceSlice attribute takes. |
| <span id="statusseen--firstseen"></span>`firstSeen` | string | no | When the radio first observed this device. |
