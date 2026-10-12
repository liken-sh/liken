---
title: Reservation
weight: 110
toc: true
---

<!-- Generated from deploy/reservations-crd.yaml by crdref. Do not edit. -->

# `Reservation`

A `Reservation` gives one holder the use of one `Telescope`. Activation runs when `spec.start` arrives, or at once with no start. Deactivation runs when `spec.end` arrives, or when the `Reservation` is deleted, and it runs the deactivation procedures, such as parking the mount, warming the cameras, and closing the dust caps, before it stops the pods. `status.steps` lists every step and its state.

## spec

The telescope, the holder, the times, the outbox, and the FITS keywords.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--telescope"></span>`telescope` | string | yes | The name of the `Telescope` to reserve. It cannot change after the reservation is created. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spec--holder"></span>`holder` | string | yes | Who uses the telescope: a person's desktop client, or `astrophotography-operator`. |
| <span id="spec--start"></span>`start` | string | no | When activation begins, as an RFC 3339 time. Optional. With no start, activation begins when the reservation is created. |
| <span id="spec--end"></span>`end` | string | no | When deactivation begins, as an RFC 3339 time. Optional. With no end, the reservation lasts until it is deleted. |
| <span id="spec--outbox"></span>`outbox` | [object](#specoutbox) | no | The volume that the telescope's imaging cameras write their frames to. Optional. Each `Camera` of the telescope mounts the claim at `/outbox`, except a camera that a `Guider` guides with, and the `Configure` step sets the driver's `UPLOAD_DIR` to the directory of the camera's name in the outbox, such as `/outbox/east-main`, and its `UPLOAD_MODE` to `UPLOAD_BOTH`, so each frame lands in the outbox and a desktop client still receives it. A camera's name is unique in its namespace, so no two drivers write to one directory. With no outbox, the operator leaves the upload settings alone, and a driver that a client sets to keep frames writes them to the pod's `/tmp`, which the pod loses when it stops. An edit takes effect at the next session. |
| <span id="spec--fitskeywords"></span>`fitsKeywords` | [\[\]object](#specfitskeywords) | no | The FITS keywords that the telescope's imaging cameras write into the header of every frame, such as `OBSERVER` or `TELESCOP`. Optional. The `Observatory`, the `Telescope`, the `Camera`, and the `Reservation` can each declare keywords, and the operator merges them in that order: a keyword at a later level replaces the value and the comment of the same name at an earlier one. The `PowerOn` step records the merged list of each `Camera` that no `Guider` guides with, and the `Configure` step writes it to the camera's `FITS_HEADER`, so the driver writes the keywords into each frame when it creates the file. An edit takes effect at the next session. The API server refuses a keyword that the driver writes from what it measures, such as `EXPTIME` or `DATE-OBS`, because a declared value would replace the measured one. |

### spec.outbox

The volume that the telescope's imaging cameras write their frames to. Optional. Each `Camera` of the telescope mounts the claim at `/outbox`, except a camera that a `Guider` guides with, and the `Configure` step sets the driver's `UPLOAD_DIR` to the directory of the camera's name in the outbox, such as `/outbox/east-main`, and its `UPLOAD_MODE` to `UPLOAD_BOTH`, so each frame lands in the outbox and a desktop client still receives it. A camera's name is unique in its namespace, so no two drivers write to one directory. With no outbox, the operator leaves the upload settings alone, and a driver that a client sets to keep frames writes them to the pod's `/tmp`, which the pod loses when it stops. An edit takes effect at the next session.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specoutbox--claimname"></span>`claimName` | string | yes | The name of a `PersistentVolumeClaim` in the reservation's namespace. The `PowerOn` step fails when it does not exist. A claim that exists and cannot be mounted on the camera's node, such as a `ReadWriteOnce` volume bound on another node, leaves the camera's pod `Pending`, and `StartDevices` gives the scheduler's message. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |

### spec.fitsKeywords[]

One keyword: its name, its value, and a comment.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specfitskeywords--name"></span>`name` | string | yes | The keyword, such as `OBSERVER`: 1 to 8 of the capital letters `A` to `Z`, the digits, `-`, and `_`. `INSTRUME` and `TELESCOP` are allowed, because the driver fills them with the names of INDI devices, such as `CCD Simulator`. |
| <span id="specfitskeywords--value"></span>`value` | string | yes | The value, at most 68 characters, the longest text that one 80-column card holds. The driver writes a value that reads as a number, such as `2026` or `-4.5`, as a number, and any other value as quoted text. A value cannot hold `/`, because the driver writes each `/` in a value as `\`. |
| <span id="specfitskeywords--comment"></span>`comment` | string | no | The comment that follows the value on the card. Optional. At most 47 characters, which fit beside a number. |

## status

What the operator observes. The operator writes it, and a person reads it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the operator last acted on. |
| <span id="status--phase"></span>`phase` | string | no | The reservation's state in one word. `Scheduled`: waiting for `spec.start`. `Activating`: the activation steps run. `Ready`: the telescope is ready, and `endpoint` names its server. `Deactivating`: the deactivation steps run. `Released`: the deactivation steps are done, and the devices are safe to power off. `Failed`: a step failed, and the `Ready` condition names it. One of: `Scheduled`, `Activating`, `Ready`, `Deactivating`, `Released`, `Failed`. |
| <span id="status--step"></span>`step` | string | no | The step that runs now, or the step that failed. It is empty while the phase is `Ready` or `Released`. One of: `Wait`, `StartSite`, `PowerOn`, `StartDevices`, `Connect`, `Configure`, `Activation`, `StartGuider`, `Abort`, `Deactivation`, `StopGuider`, `Disconnect`, `StopDevices`, `PowerOff`, `StopSite`. |
| <span id="status--steps"></span>`steps` | [\[\]object](#statussteps) | no | Every step, in the order it runs. The activation steps are `Wait`, `StartSite`, `PowerOn`, `StartDevices`, `Connect`, `Configure`, `Activation`, and `StartGuider`, and the list holds them from the start. The deactivation steps are `Abort`, `Deactivation`, `StopGuider`, `Disconnect`, `StopDevices`, `PowerOff`, and `StopSite`, and the list holds them from when deactivation begins. A step starts only when the step before it is `Done` or `Skipped`. |
| <span id="status--endpoint"></span>`endpoint` | [object](#statusendpoint) | no | The telescope's INDI server, while the phase is `Ready`. KStars connects to its `host` and `port`. |
| <span id="status--session"></span>`session` | [object](#statussession) | no | What the reservation's session started with. `StartSite` writes `observatory` and `site`, and `PowerOn` writes the rest, each once and before it creates a pod. The `observatory.liken.sh/retry` annotation on a failed activation step writes the parts again from the specs as they are then, before the step runs again, and nothing else changes them. While the reservation holds its telescope, the operator reads this record in place of the specs of the resources it names, so an edit of a device's parents, driver, power, or claim, of the `Telescope`'s `spec.observatory`, of an `OpticalTrain`, of an `OpticalTube`, of a `Guider`, of the reservation's `spec.outbox`, or of any `spec.fitsKeywords` takes effect at the next session. The record is in the reservation, so an eviction of every pod of a server leaves it, and the operator creates the pods again from it. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Ready` is `True` while the phase is `Ready`, so `kubectl wait --for=condition=Ready` waits for the telescope. When a step fails, it is `False` and its message names the step and the device. `SafeToPowerOff` is `True` when the deactivation steps are done. `EditsPending` is `True` with the reason `EditPending` while an edit of `spec.outbox` or of a `spec.fitsKeywords` waits for the next session, and its message names each change. The reservation has it from the `PowerOn` step until it is `Released`. |

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

### status.session

What the reservation's session started with. `StartSite` writes `observatory` and `site`, and `PowerOn` writes the rest, each once and before it creates a pod. The `observatory.liken.sh/retry` annotation on a failed activation step writes the parts again from the specs as they are then, before the step runs again, and nothing else changes them. While the reservation holds its telescope, the operator reads this record in place of the specs of the resources it names, so an edit of a device's parents, driver, power, or claim, of the `Telescope`'s `spec.observatory`, of an `OpticalTrain`, of an `OpticalTube`, of a `Guider`, of the reservation's `spec.outbox`, or of any `spec.fitsKeywords` takes effect at the next session. The record is in the reservation, so an eviction of every pod of a server leaves it, and the operator creates the pods again from it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussession--observatory"></span>`observatory` | string | no | The observatory of the reservation's telescope. |
| <span id="statussession--site"></span>`site` | [map\[string\]object](#statussessionsite) | no | The devices of the observatory's server, by kind and name, such as `Dome/lab`, as the server ran them when this reservation's `StartSite` began. The reservations of several telescopes in one observatory share its server, and while any of them holds its telescope, the operator reads the union of their records. Absent when the observatory has no devices. Then `observatory` alone records that the session started the observatory with none, and a device created there during the session waits for the next one. |
| <span id="statussession--devices"></span>`devices` | [map\[string\]object](#statussessiondevices) | no | The devices of the telescope's server, by kind and name, such as `Camera/east-main`, as `PowerOn` started them. |
| <span id="statussession--trains"></span>`trains` | [map\[string\]object](#statussessiontrains) | no | The spec of each `OpticalTrain` of the telescope, by name. |
| <span id="statussession--tubes"></span>`tubes` | [map\[string\]object](#statussessiontubes) | no | The spec of each `OpticalTube` of those trains, by name. A camera whose driver comes back gets the `SCOPE_INFO` of these lengths. |
| <span id="statussession--guiders"></span>`guiders` | [map\[string\]object](#statussessionguiders) | no | The spec of each `Guider` of the telescope, by name. |
| <span id="statussession--outbox"></span>`outbox` | [object](#statussessionoutbox) | no | The reservation's `spec.outbox`, which the camera pods of the session mount. |
| <span id="statussession--fitskeywords"></span>`fitsKeywords` | map[string][]object | no | The merged `spec.fitsKeywords` of each `Camera` of the session that no `Guider` guides with, by the camera's key, such as `Camera/east-main`, from the `Observatory`, the `Telescope`, the `Camera`, and the reservation. A camera whose merged list is empty has no entry. The `Configure` step writes each list to its camera's `FITS_HEADER`. |

#### status.session.site.*

One device as the session started it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessionsite--uid"></span>`uid` | string | no | The device's `metadata.uid`. A device that a person deletes and creates again with the same name during the session has another UID, and the record does not apply to it. |
| <span id="statussessionsite--telescope"></span>`telescope` | string | no | The device's `spec.telescope`. |
| <span id="statussessionsite--opticaltrain"></span>`opticalTrain` | string | no | The device's `spec.opticalTrain`. |
| <span id="statussessionsite--observatory"></span>`observatory` | string | no | The device's `spec.observatory`. |
| <span id="statussessionsite--driver"></span>`driver` | [object](#statussessionsitedriver) | no | The device's `spec.driver`. |
| <span id="statussessionsite--device"></span>`device` | [object](#statussessionsitedevice) | no | The device's `spec.device`: the resource whose driver serves it. |
| <span id="statussessionsite--power"></span>`power` | [object](#statussessionsitepower) | no | The device's `spec.power`: the output that activation switched on, and that `PowerOff` switches off. |
| <span id="statussessionsite--claim"></span>`claim` | object | no | The device's `spec.claim`, the `ResourceClaimSpec` of its pod's claim. |
| <span id="statussessionsite--guides"></span>`guides` | boolean | no | `true` for a camera that a `Guider` of the telescope guides with, whose pod runs on the node of the telescope's server. |

#### status.session.site.*.driver

The device's `spec.driver`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessionsitedriver--name"></span>`name` | string | yes | The driver's executable. |
| <span id="statussessionsitedriver--image"></span>`image` | string | no | The container image that holds the driver. |

#### status.session.site.*.device

The device's `spec.device`: the resource whose driver serves it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessionsitedevice--kind"></span>`kind` | string | yes | The kind of the resource that runs the driver. |
| <span id="statussessionsitedevice--name"></span>`name` | string | yes | The name of the resource that runs the driver. |

#### status.session.site.*.power

The device's `spec.power`: the output that activation switched on, and that `PowerOff` switches off.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessionsitepower--switch"></span>`switch` | string | yes | The name of the `Switch`. |
| <span id="statussessionsitepower--output"></span>`output` | integer | yes | The output's number. |

#### status.session.devices.*

One device as the session started it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessiondevices--uid"></span>`uid` | string | no | The device's `metadata.uid`. A device that a person deletes and creates again with the same name during the session has another UID, and the record does not apply to it. |
| <span id="statussessiondevices--telescope"></span>`telescope` | string | no | The device's `spec.telescope`. |
| <span id="statussessiondevices--opticaltrain"></span>`opticalTrain` | string | no | The device's `spec.opticalTrain`. |
| <span id="statussessiondevices--observatory"></span>`observatory` | string | no | The device's `spec.observatory`. |
| <span id="statussessiondevices--driver"></span>`driver` | [object](#statussessiondevicesdriver) | no | The device's `spec.driver`. |
| <span id="statussessiondevices--device"></span>`device` | [object](#statussessiondevicesdevice) | no | The device's `spec.device`: the resource whose driver serves it. |
| <span id="statussessiondevices--power"></span>`power` | [object](#statussessiondevicespower) | no | The device's `spec.power`: the output that activation switched on, and that `PowerOff` switches off. |
| <span id="statussessiondevices--claim"></span>`claim` | object | no | The device's `spec.claim`, the `ResourceClaimSpec` of its pod's claim. |
| <span id="statussessiondevices--guides"></span>`guides` | boolean | no | `true` for a camera that a `Guider` of the telescope guides with, whose pod runs on the node of the telescope's server. |

#### status.session.devices.*.driver

The device's `spec.driver`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessiondevicesdriver--name"></span>`name` | string | yes | The driver's executable. |
| <span id="statussessiondevicesdriver--image"></span>`image` | string | no | The container image that holds the driver. |

#### status.session.devices.*.device

The device's `spec.device`: the resource whose driver serves it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessiondevicesdevice--kind"></span>`kind` | string | yes | The kind of the resource that runs the driver. |
| <span id="statussessiondevicesdevice--name"></span>`name` | string | yes | The name of the resource that runs the driver. |

#### status.session.devices.*.power

The device's `spec.power`: the output that activation switched on, and that `PowerOff` switches off.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessiondevicespower--switch"></span>`switch` | string | yes | The name of the `Switch`. |
| <span id="statussessiondevicespower--output"></span>`output` | integer | yes | The output's number. |

#### status.session.trains.*

One train's spec.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessiontrains--uid"></span>`uid` | string | no | The train's `metadata.uid`. A train that a person deletes and creates again with the same name during the session has another UID, and the record does not apply to it. |
| <span id="statussessiontrains--telescope"></span>`telescope` | string | yes | The train's `spec.telescope`. |
| <span id="statussessiontrains--opticaltube"></span>`opticalTube` | string | yes | The train's `spec.opticalTube`. |

#### status.session.tubes.*

One tube's spec.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessiontubes--uid"></span>`uid` | string | no | The tube's `metadata.uid`. A tube that a person deletes and creates again with the same name during the session has another UID, and the record does not apply to it. |
| <span id="statussessiontubes--telescope"></span>`telescope` | string | yes | The tube's `spec.telescope`. |
| <span id="statussessiontubes--aperture"></span>`aperture` | number | yes | The tube's `spec.aperture`, in millimeters. |
| <span id="statussessiontubes--focallength"></span>`focalLength` | number | yes | The tube's `spec.focalLength`, in millimeters. |

#### status.session.guiders.*

One `Guider`'s spec.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessionguiders--uid"></span>`uid` | string | no | The `Guider`'s `metadata.uid`. A `Guider` that a person deletes and creates again with the same name during the session has another UID, and the record does not apply to it. |
| <span id="statussessionguiders--telescope"></span>`telescope` | string | yes | The `Guider`'s `spec.telescope`. |
| <span id="statussessionguiders--opticaltrain"></span>`opticalTrain` | string | yes | The `Guider`'s `spec.opticalTrain`. |
| <span id="statussessionguiders--pulses"></span>`pulses` | string | yes | The `Guider`'s `spec.pulses`. One of: `Mount`, `Camera`. |

#### status.session.outbox

The reservation's `spec.outbox`, which the camera pods of the session mount.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessionoutbox--claimname"></span>`claimName` | string | yes | The name of the `PersistentVolumeClaim`. |

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
