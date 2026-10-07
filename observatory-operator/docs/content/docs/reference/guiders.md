---
title: Guider
weight: 160
toc: true
---

<!-- Generated from deploy/guiders-crd.yaml by crdref. Do not edit. -->

# `Guider`

A `Guider` runs PHD2 for one `Telescope`, as a client of the telescope's INDI server, and guides with the camera of one `OpticalTrain`. A guide scope is a train with its own small tube, and an off-axis guider is a train on the imaging train's tube. While a `Reservation` holds the telescope, the operator runs PHD2, connects it to the guide camera and the mount, and leaves it idle. The holder calibrates and guides through PHD2's event server at `status.endpoint`.

## spec

What the guider guides, and how.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--telescope"></span>`telescope` | string | yes | The name of the `Telescope` that the guider corrects. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spec--opticaltrain"></span>`opticalTrain` | string | yes | The name of the `OpticalTrain` whose camera guides. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spec--pulses"></span>`pulses` | string | yes | Where PHD2 sends its corrections: `Mount` sends them to the mount's driver, and `Camera` sends them through the guide camera's ST-4 port. One of: `Mount`, `Camera`. |

## status

What the operator observes. The operator writes it, and a person reads it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the operator last acted on. |
| <span id="status--endpoint"></span>`endpoint` | [object](#statusendpoint) | no | PHD2's event server, while the guider's pod exists. KStars and `astrophotography-operator` drive PHD2 at its `host` and `port`. |
| <span id="status--pod"></span>`pod` | string | no | The name of the guider's pod. |
| <span id="status--node"></span>`node` | string | no | The node that runs the guider's pod. |
| <span id="status--state"></span>`state` | string | no | PHD2's state, as its event server names it, while the operator's connection to that server is open. `Stopped`: no exposures run. `Selected`: a star is selected, and no exposures run. `Looping`: PHD2 takes exposures and does not guide. `Calibrating`, `Guiding`, and `Paused` name what PHD2 does. `LostLock`: PHD2 guides and lost its star. One of: `Stopped`, `Selected`, `Calibrating`, `Guiding`, `LostLock`, `Paused`, `Looping`. |
| <span id="status--calibrated"></span>`calibrated` | boolean | no | `true` while PHD2 holds a calibration of the mount. |
| <span id="status--pixelscale"></span>`pixelScale` | number | no | The guide camera's scale in arc-seconds per pixel, from the camera's pixel size and the guide tube's focal length. It is absent while PHD2 does not know one of them. |
| <span id="status--rms"></span>`rms` | [object](#statusrms) | no | The root mean square of the star's distance from the lock position over the last guide steps, in arc-seconds, along the mount's axes and in total. It covers at most the last 100 steps that the operator received since guiding started, and `since` gives the time of the first of them. PHD2 sends a new connection no earlier steps, so the window also starts again when the operator restarts. It is absent while PHD2's pixel scale is not known. |
| <span id="status--star"></span>`star` | [object](#statusstar) | no | The guide star, as PHD2 measured it in the last guide step. |
| <span id="status--laststeptime"></span>`lastStepTime` | string | no | When PHD2 sent the last guide step. |
| <span id="status--alert"></span>`alert` | [object](#statusalert) | no | The last alert that PHD2 showed, or the last calibration that failed. |
| <span id="status--display"></span>`display` | [object](#statusdisplay) | no | What the printer columns show. |
| <span id="status--phase"></span>`phase` | string | no | The state in one word. `Idle`: no reservation needs it, and nothing runs. `Activating`: a reservation's activation steps run. `Ready`: everything it needs runs and is connected. `Deactivating`: a reservation's deactivation steps run. `Error`: a step failed, and the `Ready` condition gives the reason. One of: `Idle`, `Activating`, `Ready`, `Deactivating`, `Error`. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Ready` is `True` while PHD2 runs and its camera and mount are connected. `ParentFound` is `False` when the `Telescope` or the `OpticalTrain` does not exist. |

### status.endpoint

PHD2's event server, while the guider's pod exists. KStars and `astrophotography-operator` drive PHD2 at its `host` and `port`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusendpoint--service"></span>`service` | string | yes | The name of the guider's `Service`. |
| <span id="statusendpoint--host"></span>`host` | string | yes | The `Service`'s DNS name in the cluster. |
| <span id="statusendpoint--port"></span>`port` | integer | yes | The port of PHD2's event server, 4400. |

### status.rms

The root mean square of the star's distance from the lock position over the last guide steps, in arc-seconds, along the mount's axes and in total. It covers at most the last 100 steps that the operator received since guiding started, and `since` gives the time of the first of them. PHD2 sends a new connection no earlier steps, so the window also starts again when the operator restarts. It is absent while PHD2's pixel scale is not known.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusrms--ra"></span>`ra` | number | yes | The RMS in right ascension, in arc-seconds. |
| <span id="statusrms--dec"></span>`dec` | number | yes | The RMS in declination, in arc-seconds. |
| <span id="statusrms--total"></span>`total` | number | yes | The RMS of both axes, in arc-seconds. |
| <span id="statusrms--steps"></span>`steps` | integer | yes | The number of guide steps that the RMS covers. |
| <span id="statusrms--since"></span>`since` | string | yes | When PHD2 sent the first guide step that the RMS covers, as an RFC 3339 time to the second. |

### status.star

The guide star, as PHD2 measured it in the last guide step.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstar--snr"></span>`snr` | number | yes | The star's signal-to-noise ratio. |
| <span id="statusstar--hfd"></span>`hfd` | number | yes | The star's half-flux diameter, in pixels. |

### status.alert

The last alert that PHD2 showed, or the last calibration that failed.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusalert--message"></span>`message` | string | yes | PHD2's text. |
| <span id="statusalert--type"></span>`type` | string | yes | `info`, `question`, `warning`, or `error`. |
| <span id="statusalert--time"></span>`time` | string | yes | When PHD2 sent the alert. |

### status.display

What the printer columns show.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdisplay--rms"></span>`rms` | string | no | `rms.total` with its unit, such as `0.84 arcsec`. |

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
