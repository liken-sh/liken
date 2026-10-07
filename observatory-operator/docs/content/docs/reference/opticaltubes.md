---
title: OpticalTube
weight: 115
toc: true
---

<!-- Generated from deploy/opticaltubes-crd.yaml by crdref. Do not edit. -->

# `OpticalTube`

An `OpticalTube` is the optical tube assembly, or OTA: the lens or mirror that collects the light. It has no driver. Two OpticalTrains can name one tube, such as an imaging train and an off-axis guider.

## spec

The tube's optics.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--telescope"></span>`telescope` | string | yes | The name of the `Telescope` that the tube is on. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spec--aperture"></span>`aperture` | number | yes | The diameter of the lens or the primary mirror, in millimeters. |
| <span id="spec--focallength"></span>`focalLength` | number | yes | The focal length, in millimeters, with any reducer or barlow that stays on the tube. |

## status

What the operator observes. The operator writes it, and a person reads it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the operator last acted on. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `ParentFound` is `False` when the `Telescope` or its `Observatory` does not exist. |
| <span id="status--display"></span>`display` | [object](#statusdisplay) | no | The spec as a person reads it, with its units, for the printer columns. |
| <span id="status--trains"></span>`trains` | []string | no | Each `OpticalTrain` that names this tube. |

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

### status.display

The spec as a person reads it, with its units, for the printer columns.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdisplay--aperture"></span>`aperture` | string | no | The aperture, such as `80 mm`. |
| <span id="statusdisplay--focallength"></span>`focalLength` | string | no | The focal length, such as `480 mm`. |
