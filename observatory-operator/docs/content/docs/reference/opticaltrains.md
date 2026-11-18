---
title: OpticalTrain
weight: 120
toc: true
---

<!-- Generated from deploy/opticaltrains-crd.yaml by crdref. Do not edit. -->

# `OpticalTrain`

An `OpticalTrain` is one light path: an `OpticalTube`, and the camera, filter wheel, focuser, and rotator behind it. A device joins the train when its `spec.opticalTrain` is the train's name. The operator writes each camera's INDI `ACTIVE_DEVICES` property so that the camera reads the focuser, filter wheel, and rotator of its own train and the mount of its telescope. It also writes the tube's aperture and focal length into the camera's `SCOPE_INFO`.

## spec

The telescope the train is on, and its tube.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--telescope"></span>`telescope` | string | yes | The name of the `Telescope` that the train is on. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spec--opticaltube"></span>`opticalTube` | string | yes | The name of the `OpticalTube` that collects the train's light. Two trains can use the same tube. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |

## status

What the operator observes. Only the operator writes it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the operator last acted on. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | The `ParentFound` condition is `False` when the `Telescope`, its `Observatory`, or the `OpticalTube` does not exist. |
| <span id="status--devices"></span>`devices` | [\[\]object](#statusdevices) | no | Each device whose `spec.opticalTrain` is this train's name. While a reservation's session holds a device, the list uses the train that the session recorded when it started, because a change to `spec.opticalTrain` takes effect when the session ends. |

### status.conditions[]

One condition, in the shape of `metav1.Condition`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's name, such as `Ready`. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the condition is met: `True`, `False`, or `Unknown`. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the condition reflects. |
| <span id="statusconditions--reason"></span>`reason` | string | yes | One word in CamelCase for the cause. |
| <span id="statusconditions--message"></span>`message` | string | yes | A sentence for a person that explains the reason. It can be empty. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When `status` last changed. A write that leaves `status` unchanged keeps this time. |

### status.devices[]

One device and its phase.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdevices--kind"></span>`kind` | string | yes | The device's kind. One of: `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `Switch`, `Receiver`. |
| <span id="statusdevices--name"></span>`name` | string | yes | The device's name. |
| <span id="statusdevices--phase"></span>`phase` | string | no | The device's `status.phase`. One of: `Inventory`, `Idle`, `Starting`, `Connecting`, `Connected`, `Disconnecting`, `Error`. |
