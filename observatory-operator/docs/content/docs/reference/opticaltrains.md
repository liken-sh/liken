---
title: OpticalTrain
weight: 120
toc: true
---

<!-- Generated from deploy/opticaltrains-crd.yaml by crdref. Do not edit. -->

An `OpticalTrain` is one light path: an `OpticalTube`, and the devices behind it, which name the train in their `spec.opticalTrain`. The operator writes each camera's `ACTIVE_DEVICES` from its train, so the train sets which mount, focuser, filter wheel, and rotator the camera snoops.

## spec

The train's place and its tube.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--telescope"></span>`telescope` | string | yes | The name of the `Telescope` that the train is on. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spec--opticaltube"></span>`opticalTube` | string | yes | The name of the `OpticalTube` that collects the train's light. Two trains can name one tube. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |

## status

What the operator observes. The operator writes it, and a person reads it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the operator last acted on. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `ParentFound` is `False` when the `Telescope`, its `Observatory`, or the `OpticalTube` does not exist. |
| <span id="status--devices"></span>`devices` | [\[\]object](#statusdevices) | no | Each device whose `spec.opticalTrain` names this train. |

### status.conditions[]

One condition, in the shape of `metav1.Condition`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's name, such as `Ready`. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the condition holds: `True`, `False`, or `Unknown`. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the condition judged. |
| <span id="statusconditions--reason"></span>`reason` | string | yes | One word in CamelCase for the cause. |
| <span id="statusconditions--message"></span>`message` | string | yes | The cause, for a person to read. It can be empty. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When `status` last changed. A write with the same status keeps this time. |

### status.devices[]

One device, with its phase.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdevices--kind"></span>`kind` | string | yes | The device's kind. One of: `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `SkyQualityMeter`, `Switch`, `Receiver`. |
| <span id="statusdevices--name"></span>`name` | string | yes | The device's name. |
| <span id="statusdevices--phase"></span>`phase` | string | no | The device's `status.phase`. One of: `Inventory`, `Idle`, `Starting`, `Connecting`, `Connected`, `Disconnecting`, `Error`. |
