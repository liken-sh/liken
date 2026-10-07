---
title: Reservation
weight: 110
toc: true
---

<!-- Generated from deploy/reservations-crd.yaml by crdref. Do not edit. -->

# `Reservation`

A `Reservation` gives one holder the use of one `Telescope`. Activation runs when `spec.start` arrives, or at once with no start. Deactivation runs when `spec.end` arrives, or when the `Reservation` is deleted, and it runs the deactivation procedures, such as parking the mount, warming the cameras, and closing the dust caps, before it stops the pods. `status.steps` lists every step and its state.

## spec

The telescope, the holder, and the times.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--telescope"></span>`telescope` | string | yes | The name of the `Telescope` to reserve. It cannot change after the reservation is created. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spec--holder"></span>`holder` | string | yes | Who uses the telescope: a person's desktop client, or a `Session` of `astrophotography-operator`. |
| <span id="spec--start"></span>`start` | string | no | When activation begins, as an RFC 3339 time. Optional. With no start, activation begins when the reservation is created. |
| <span id="spec--end"></span>`end` | string | no | When deactivation begins, as an RFC 3339 time. Optional. With no end, the reservation lasts until it is deleted. |

## status

What the operator observes. The operator writes it, and a person reads it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the operator last acted on. |
| <span id="status--phase"></span>`phase` | string | no | The reservation's state in one word. `Scheduled`: waiting for `spec.start`. `Activating`: the activation steps run. `Ready`: the telescope is ready, and `endpoint` names its server. `Deactivating`: the deactivation steps run. `Released`: the deactivation steps are done, and the devices are safe to power off. `Failed`: a step failed, and the `Ready` condition names it. One of: `Scheduled`, `Activating`, `Ready`, `Deactivating`, `Released`, `Failed`. |
| <span id="status--step"></span>`step` | string | no | The step that runs now, or the step that failed. It is empty while the phase is `Ready` or `Released`. One of: `Wait`, `StartSite`, `PowerOn`, `StartDevices`, `Connect`, `Configure`, `Activation`, `StartGuider`, `Abort`, `Deactivation`, `StopGuider`, `Disconnect`, `StopDevices`, `PowerOff`, `StopSite`. |
| <span id="status--steps"></span>`steps` | [\[\]object](#statussteps) | no | Every step, in the order it runs. The activation steps are `Wait`, `StartSite`, `PowerOn`, `StartDevices`, `Connect`, `Configure`, `Activation`, and `StartGuider`, and the list holds them from the start. The deactivation steps are `Abort`, `Deactivation`, `StopGuider`, `Disconnect`, `StopDevices`, `PowerOff`, and `StopSite`, and the list holds them from when deactivation begins. A step starts only when the step before it is `Done` or `Skipped`. |
| <span id="status--endpoint"></span>`endpoint` | [object](#statusendpoint) | no | The telescope's INDI server, while the phase is `Ready`. KStars connects to its `host` and `port`. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Ready` is `True` while the phase is `Ready`, so `kubectl wait --for=condition=Ready` waits for the telescope. When a step fails, it is `False` and its message names the step and the device. `SafeToPowerOff` is `True` when the deactivation steps are done. |

### status.steps[]

One step.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussteps--name"></span>`name` | string | yes | The step's name. One of: `Wait`, `StartSite`, `PowerOn`, `StartDevices`, `Connect`, `Configure`, `Activation`, `StartGuider`, `Abort`, `Deactivation`, `StopGuider`, `Disconnect`, `StopDevices`, `PowerOff`, `StopSite`. |
| <span id="statussteps--state"></span>`state` | string | yes | The step's progress: `Pending`, `Running`, `Done`, `Failed`, or `Skipped` when it has nothing to do. One of: `Pending`, `Running`, `Done`, `Failed`, `Skipped`. |
| <span id="statussteps--starttime"></span>`startTime` | string | no | When the step began to run. |
| <span id="statussteps--stoptime"></span>`stopTime` | string | no | When the step ended. |
| <span id="statussteps--summary"></span>`summary` | string | no | What the step waits for or did. It names the device when one device holds the step up. |
| <span id="statussteps--actions"></span>`actions` | [\[\]object](#statusstepsactions) | no | The actions of the procedures that the `Activation` or `Deactivation` step waited on, each with the resource it belongs to. The resource's `status.procedures` holds the same record. |

#### status.steps[].actions[]

One action of one resource.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstepsactions--kind"></span>`kind` | string | yes | The kind of the resource. |
| <span id="statusstepsactions--name"></span>`name` | string | yes | The name of the resource. |
| <span id="statusstepsactions--action"></span>`action` | string | yes | The action as its YAML reads, such as `state: Parked` or `cool: -10 °C within 0.5 °C`. |
| <span id="statusstepsactions--state"></span>`state` | string | yes | The action's progress: `Pending`, `Running`, `Done`, `Failed`, or `Skipped` when it had nothing to do. One of: `Pending`, `Running`, `Done`, `Failed`, `Skipped`. |
| <span id="statusstepsactions--starttime"></span>`startTime` | string | no | When the action began to run. |
| <span id="statusstepsactions--stoptime"></span>`stopTime` | string | no | When the action ended. |
| <span id="statusstepsactions--summary"></span>`summary` | string | no | What the action waits for or did. |

### status.endpoint

The telescope's INDI server, while the phase is `Ready`. KStars connects to its `host` and `port`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusendpoint--service"></span>`service` | string | yes | The name of the server's `Service`. |
| <span id="statusendpoint--host"></span>`host` | string | yes | The `Service`'s DNS name in the cluster. |
| <span id="statusendpoint--port"></span>`port` | integer | yes | The INDI port of the `Service`, 7624. |

### status.conditions[]

One condition, in the shape of `metav1.Condition`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's name, such as `Ready`. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the condition holds: `True`, `False`, or `Unknown`. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the condition reflects. |
| <span id="statusconditions--reason"></span>`reason` | string | yes | One word in CamelCase for the cause. |
| <span id="statusconditions--message"></span>`message` | string | yes | The cause, for a person to read. It can be empty. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When `status` last changed. A write with the same status keeps this time. |
