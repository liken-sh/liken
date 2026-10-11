---
title: Camera
weight: 130
toc: true
---

<!-- Generated from deploy/cameras-crd.yaml by crdref. Do not edit. -->

# `Camera`

A `Camera` is an imaging or guiding camera: an INDI driver with `CCD_INTERFACE`. It belongs to one `OpticalTrain`, and the operator writes the camera's `ACTIVE_DEVICES` from that train, so the camera snoops the mount, focuser, filter wheel, and rotator of its own light path.

## spec

One camera, with INDI's `CCD_INTERFACE`. The operator writes each setting that the spec states when a reservation configures the camera, and leaves the others as the driver sets them.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--opticaltrain"></span>`opticalTrain` | string | no | The name of the `OpticalTrain` that the device is on. The train's members set what each camera on it snoops. Optional. With no `opticalTrain`, the device is on the shelf, and the operator creates nothing for it. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spec--driver"></span>`driver` | [object](#specdriver) | no | The INDI driver that runs the device. With no `image`, the operator runs the image that the `indi` build lists for the driver, so `name` must then be an INDI driver that the image holds, such as `indi_simulator_ccd`. Set `driver` or `device`, not both. |
| <span id="spec--device"></span>`device` | [object](#specdevice) | no | The resource whose driver serves this device, in place of a driver of its own. INDI puts every interface of one driver on one device, so a camera with a built-in filter wheel serves a `FilterWheel`, and a flat panel with a motorized cover serves a `DustCap`. The resource it names runs the driver, holds the pod, the claim, and the power, and must be on the same INDI server. The operator connects that device before this one's activation, and runs this one's deactivation before that device stops. Set `driver` or `device`, not both. The field cannot change, and a resource that runs a driver cannot gain it: delete the resource and create it again to move it to another device. |
| <span id="spec--power"></span>`power` | [object](#specpower) | no | The `Switch` output that powers the device. Activation switches the output on before the device's pod starts, and deactivation switches it off after the pod stops. Optional. With no power, a person powers the device on and off by hand. |
| <span id="spec--claim"></span>`claim` | object | no | A `ResourceClaimSpec` of the `resource.k8s.io` API, for real hardware. The operator creates a `ResourceClaim` from it for the device's pod. The API server validates it when the operator creates the claim. Optional. A simulator needs no claim. |
| <span id="spec--temperatureramp"></span>`temperatureRamp` | [object](#spectemperatureramp) | no | How fast the driver changes the cooler's setpoint toward a new one. The operator writes it to `CCD_TEMP_RAMP` once, in the `Configure` step of each reservation. Optional. |
| <span id="spec--captureformat"></span>`captureFormat` | string | no | The format that the sensor captures in, such as mono or a raw Bayer pattern. The operator writes it to `CCD_CAPTURE_FORMAT` once, in the `Configure` step of each reservation. Optional. It names one of the elements that the driver defines, such as `INDI_MONO`. |
| <span id="spec--transferformat"></span>`transferFormat` | string | no | The format that the driver sends each frame in: FITS, XISF, or the camera's native format. The operator writes it to `CCD_TRANSFER_FORMAT` once, in the `Configure` step of each reservation. Optional. One of: `FORMAT_FITS`, `FORMAT_NATIVE`, `FORMAT_XISF`. |
| <span id="spec--compression"></span>`compression` | string | no | Whether the driver compresses each frame of the sensor before it sends it. The operator writes it to `CCD_COMPRESSION` once, in the `Configure` step of each reservation. Optional. One of: `INDI_ENABLED`, `INDI_DISABLED`. |
| <span id="spec--guidercompression"></span>`guiderCompression` | string | no | Whether the driver compresses each frame of the guide head before it sends it. The operator writes it to `GUIDER_COMPRESSION` once, in the `Configure` step of each reservation. Optional. One of: `INDI_ENABLED`, `INDI_DISABLED`. |
| <span id="spec--wcs"></span>`wcs` | string | no | Whether the driver writes the world coordinate system keywords into the FITS header of each frame. The operator writes it to `WCS_CONTROL` once, in the `Configure` step of each reservation. Optional. One of: `WCS_ENABLE`, `WCS_DISABLE`. |
| <span id="spec--gain"></span>`gain` | number | no | The sensor's gain, in the driver's own units. The operator writes it to `CCD_GAIN` once, in the `Configure` step of each reservation. Optional. |
| <span id="spec--offset"></span>`offset` | number | no | The level that the sensor adds to each pixel, in the driver's own units. The operator writes it to `CCD_OFFSET` once, in the `Configure` step of each reservation. Optional. |
| <span id="spec--activation"></span>`activation` | [\[\]object](#specactivation) | no | The actions that run, in order, as this resource's `Telescope` or `Observatory` turns `Active`, while every device is still connected. For a `Telescope` or an `Observatory`, that is the resource itself. The `Reservation`'s `Activation` step waits for them. Each action is a target state, so a run that an operator restart interrupted runs again each action that is not `Done`. Optional. |
| <span id="spec--deactivation"></span>`deactivation` | [\[\]object](#specdeactivation) | no | The actions that run, in order, as this resource's `Telescope` or `Observatory` stops being `Active`, while every device is still connected. For a `Telescope` or an `Observatory`, that is the resource itself. The `Reservation`'s `Deactivation` step waits for them. Each action is a target state, so a run that an operator restart interrupted runs again each action that is not `Done`. Optional. |
| <span id="spec--triggers"></span>`triggers` | [\[\]object](#spectriggers) | no | Triggers on conditions. Each one runs its actions once for each transition of its condition to the status it names, while the resource is active: from when its activation is done until its deactivation begins. A condition that holds when the activation ends runs then. A run that began runs to its end, unless the resource's deactivation begins, and a resource runs one procedure at a time. Optional. |

### spec.driver

The INDI driver that runs the device. With no `image`, the operator runs the image that the `indi` build lists for the driver, so `name` must then be an INDI driver that the image holds, such as `indi_simulator_ccd`. Set `driver` or `device`, not both.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdriver--name"></span>`name` | string | yes | The driver's executable, such as `indi_simulator_ccd`. The device's pod runs it under `socat`, so the name holds no path, no space, no comma, and no colon. Pattern: `^[A-Za-z0-9_][A-Za-z0-9_.+-]*$`. |
| <span id="specdriver--image"></span>`image` | string | no | The container image that holds the driver, used as written. The image must hold `/usr/bin/socat` and the driver on `PATH`, and must run as user 1000 with a read-only root filesystem and a writable `/tmp`. An image built `FROM ghcr.io/liken-sh/indi` meets all three. Optional. |

### spec.device

The resource whose driver serves this device, in place of a driver of its own. INDI puts every interface of one driver on one device, so a camera with a built-in filter wheel serves a `FilterWheel`, and a flat panel with a motorized cover serves a `DustCap`. The resource it names runs the driver, holds the pod, the claim, and the power, and must be on the same INDI server. The operator connects that device before this one's activation, and runs this one's deactivation before that device stops. Set `driver` or `device`, not both. The field cannot change, and a resource that runs a driver cannot gain it: delete the resource and create it again to move it to another device.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdevice--kind"></span>`kind` | string | yes | The kind of the resource that runs the driver, such as `Camera`. One of: `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `Switch`, `Receiver`. |
| <span id="specdevice--name"></span>`name` | string | yes | The name of that resource in this namespace. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |

### spec.power

The `Switch` output that powers the device. Activation switches the output on before the device's pod starts, and deactivation switches it off after the pod stops. Optional. With no power, a person powers the device on and off by hand.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specpower--switch"></span>`switch` | string | yes | The name of a `Switch` in this namespace. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="specpower--output"></span>`output` | integer | yes | The number of the output, from 1, as the driver numbers `DIGITAL_OUTPUT_1`. |

### spec.temperatureRamp

How fast the driver changes the cooler's setpoint toward a new one. The operator writes it to `CCD_TEMP_RAMP` once, in the `Configure` step of each reservation. Optional.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectemperatureramp--slope"></span>`slope` | number | no | The greatest change of the setpoint, in degrees Celsius per minute. Zero turns the ramp off. The member is `RAMP_SLOPE`. |
| <span id="spectemperatureramp--threshold"></span>`threshold` | number | no | How close to the setpoint the sensor must come before the change is done, in degrees Celsius. The member is `RAMP_THRESHOLD`. |

### spec.activation[]

One action, which names exactly one of `cool`, `warm`, and `job`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specactivation--cool"></span>`cool` | [object](#specactivationcool) | no | Cools the sensor: the operator writes `celsius` to `CCD_TEMPERATURE` and waits until the sensor is within `within` of it. The default timeout is 20 minutes. |
| <span id="specactivation--warm"></span>`warm` | [object](#specactivationwarm) | no | Warms the sensor before its cooler goes off: the operator writes `celsius` to `CCD_TEMPERATURE`, waits until the sensor is within `within` of it or until the timeout, and switches `CCD_COOLER` off. A cooler cannot warm a sensor above the air around it, so the timeout ends the wait and does not fail the action. The default timeout is 10 minutes. |
| <span id="specactivation--job"></span>`job` | [object](#specactivationjob) | no | A container that the operator runs once as a Kubernetes `Job` in the resource's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes. |
| <span id="specactivation--timeout"></span>`timeout` | string | no | How long the action may take, its waits for `requires` and `after` included, as a duration such as `20m`. An action that passes it fails. Optional. Each action has a default. |
| <span id="specactivation--requires"></span>`requires` | [\[\]object](#specactivationrequires) | no | The conditions that must hold before the action runs. The operator waits for each one until the action's timeout. They bind only the actions that the operator runs: a move that a person makes in KStars is stopped only by the drivers' park locks. |
| <span id="specactivation--after"></span>`after` | [\[\]object](#specactivationafter) | no | The resources whose runs for the same event must end before the action runs: the same lifecycle step, or the same transition of the same condition. A resource with no such run is not waited for. A resource in a later tier of the tree is waited for until the action's timeout, and the action then fails. |

#### spec.activation[].cool

Cools the sensor: the operator writes `celsius` to `CCD_TEMPERATURE` and waits until the sensor is within `within` of it. The default timeout is 20 minutes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specactivationcool--celsius"></span>`celsius` | number | yes | The setpoint, in degrees Celsius. |
| <span id="specactivationcool--within"></span>`within` | number | no | How close to the setpoint the sensor must come, in degrees Celsius. The default is 0.5. Default: `0.5`. |

#### spec.activation[].warm

Warms the sensor before its cooler goes off: the operator writes `celsius` to `CCD_TEMPERATURE`, waits until the sensor is within `within` of it or until the timeout, and switches `CCD_COOLER` off. A cooler cannot warm a sensor above the air around it, so the timeout ends the wait and does not fail the action. The default timeout is 10 minutes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specactivationwarm--celsius"></span>`celsius` | number | yes | The setpoint, in degrees Celsius. |
| <span id="specactivationwarm--within"></span>`within` | number | no | How close to the setpoint the sensor must come, in degrees Celsius. The default is 0.5. Default: `0.5`. |

#### spec.activation[].job

A container that the operator runs once as a Kubernetes `Job` in the resource's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specactivationjob--image"></span>`image` | string | yes | The container image, used as written, such as `busybox:1.37`. |
| <span id="specactivationjob--command"></span>`command` | []string | no | The command that replaces the image's entrypoint, as a container's `command` does. Optional. |
| <span id="specactivationjob--args"></span>`args` | []string | no | The arguments of the command, as a container's `args` are. Optional. |
| <span id="specactivationjob--env"></span>`env` | [\[\]object](#specactivationjobenv) | no | Variables that the operator adds to the container's environment before its own, as `{name, value}`. A variable that has the name of one of the operator's own is replaced by the operator's. Optional. |

#### spec.activation[].job.env[]

One variable.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specactivationjobenv--name"></span>`name` | string | yes | The variable's name, such as `RELAY_URL`. Pattern: `^[-._a-zA-Z][-._a-zA-Z0-9]*$`. |
| <span id="specactivationjobenv--value"></span>`value` | string | yes | The variable's value. It can be empty. |

#### spec.activation[].requires[]

One condition, and the status it must have.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specactivationrequires--kind"></span>`kind` | string | no | The kind of the resource whose condition it reads. With no `kind`, the condition is the resource's own. A `kind` of `Observatory` or `Telescope` with no `name` means the resource's own observatory or telescope. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `Switch`, `Receiver`, `Guider`. |
| <span id="specactivationrequires--name"></span>`name` | string | no | The name of the resource, in this namespace. A device kind needs a name. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="specactivationrequires--type"></span>`type` | string | yes | The condition's type, such as `Safe` or `Parked`. |
| <span id="specactivationrequires--status"></span>`status` | string | no | The status that the condition must have: `True`, `False`, or `Unknown`. The default is `True`. One of: `True`, `False`, `Unknown`. Default: `True`. |

#### spec.activation[].after[]

One resource, by its kind and name.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specactivationafter--kind"></span>`kind` | string | yes | The kind of the resource. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `Switch`, `Receiver`, `Guider`. |
| <span id="specactivationafter--name"></span>`name` | string | no | The name of the resource. With no `name`, the reference names every resource of the kind in the observatory, or the resource's own `Observatory` or `Telescope`. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |

### spec.deactivation[]

One action, which names exactly one of `cool`, `warm`, and `job`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdeactivation--cool"></span>`cool` | [object](#specdeactivationcool) | no | Cools the sensor: the operator writes `celsius` to `CCD_TEMPERATURE` and waits until the sensor is within `within` of it. The default timeout is 20 minutes. |
| <span id="specdeactivation--warm"></span>`warm` | [object](#specdeactivationwarm) | no | Warms the sensor before its cooler goes off: the operator writes `celsius` to `CCD_TEMPERATURE`, waits until the sensor is within `within` of it or until the timeout, and switches `CCD_COOLER` off. A cooler cannot warm a sensor above the air around it, so the timeout ends the wait and does not fail the action. The default timeout is 10 minutes. |
| <span id="specdeactivation--job"></span>`job` | [object](#specdeactivationjob) | no | A container that the operator runs once as a Kubernetes `Job` in the resource's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes. |
| <span id="specdeactivation--timeout"></span>`timeout` | string | no | How long the action may take, its waits for `requires` and `after` included, as a duration such as `20m`. An action that passes it fails. Optional. Each action has a default. |
| <span id="specdeactivation--requires"></span>`requires` | [\[\]object](#specdeactivationrequires) | no | The conditions that must hold before the action runs. The operator waits for each one until the action's timeout. They bind only the actions that the operator runs: a move that a person makes in KStars is stopped only by the drivers' park locks. |
| <span id="specdeactivation--after"></span>`after` | [\[\]object](#specdeactivationafter) | no | The resources whose runs for the same event must end before the action runs: the same lifecycle step, or the same transition of the same condition. A resource with no such run is not waited for. A resource in a later tier of the tree is waited for until the action's timeout, and the action then fails. |

#### spec.deactivation[].cool

Cools the sensor: the operator writes `celsius` to `CCD_TEMPERATURE` and waits until the sensor is within `within` of it. The default timeout is 20 minutes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdeactivationcool--celsius"></span>`celsius` | number | yes | The setpoint, in degrees Celsius. |
| <span id="specdeactivationcool--within"></span>`within` | number | no | How close to the setpoint the sensor must come, in degrees Celsius. The default is 0.5. Default: `0.5`. |

#### spec.deactivation[].warm

Warms the sensor before its cooler goes off: the operator writes `celsius` to `CCD_TEMPERATURE`, waits until the sensor is within `within` of it or until the timeout, and switches `CCD_COOLER` off. A cooler cannot warm a sensor above the air around it, so the timeout ends the wait and does not fail the action. The default timeout is 10 minutes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdeactivationwarm--celsius"></span>`celsius` | number | yes | The setpoint, in degrees Celsius. |
| <span id="specdeactivationwarm--within"></span>`within` | number | no | How close to the setpoint the sensor must come, in degrees Celsius. The default is 0.5. Default: `0.5`. |

#### spec.deactivation[].job

A container that the operator runs once as a Kubernetes `Job` in the resource's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdeactivationjob--image"></span>`image` | string | yes | The container image, used as written, such as `busybox:1.37`. |
| <span id="specdeactivationjob--command"></span>`command` | []string | no | The command that replaces the image's entrypoint, as a container's `command` does. Optional. |
| <span id="specdeactivationjob--args"></span>`args` | []string | no | The arguments of the command, as a container's `args` are. Optional. |
| <span id="specdeactivationjob--env"></span>`env` | [\[\]object](#specdeactivationjobenv) | no | Variables that the operator adds to the container's environment before its own, as `{name, value}`. A variable that has the name of one of the operator's own is replaced by the operator's. Optional. |

#### spec.deactivation[].job.env[]

One variable.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdeactivationjobenv--name"></span>`name` | string | yes | The variable's name, such as `RELAY_URL`. Pattern: `^[-._a-zA-Z][-._a-zA-Z0-9]*$`. |
| <span id="specdeactivationjobenv--value"></span>`value` | string | yes | The variable's value. It can be empty. |

#### spec.deactivation[].requires[]

One condition, and the status it must have.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdeactivationrequires--kind"></span>`kind` | string | no | The kind of the resource whose condition it reads. With no `kind`, the condition is the resource's own. A `kind` of `Observatory` or `Telescope` with no `name` means the resource's own observatory or telescope. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `Switch`, `Receiver`, `Guider`. |
| <span id="specdeactivationrequires--name"></span>`name` | string | no | The name of the resource, in this namespace. A device kind needs a name. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="specdeactivationrequires--type"></span>`type` | string | yes | The condition's type, such as `Safe` or `Parked`. |
| <span id="specdeactivationrequires--status"></span>`status` | string | no | The status that the condition must have: `True`, `False`, or `Unknown`. The default is `True`. One of: `True`, `False`, `Unknown`. Default: `True`. |

#### spec.deactivation[].after[]

One resource, by its kind and name.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdeactivationafter--kind"></span>`kind` | string | yes | The kind of the resource. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `Switch`, `Receiver`, `Guider`. |
| <span id="specdeactivationafter--name"></span>`name` | string | no | The name of the resource. With no `name`, the reference names every resource of the kind in the observatory, or the resource's own `Observatory` or `Telescope`. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |

### spec.triggers[]

One trigger: a condition, and the actions it runs.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectriggers--when"></span>`when` | [object](#spectriggerswhen) | yes | The condition, and the status that fires the trigger. |
| <span id="spectriggers--run"></span>`run` | [\[\]object](#spectriggersrun) | yes | The actions that run, in order. |

#### spec.triggers[].when

The condition, and the status that fires the trigger.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectriggerswhen--kind"></span>`kind` | string | no | The kind of the resource whose condition it reads. With no `kind`, the condition is the resource's own. A `kind` of `Observatory` or `Telescope` with no `name` means the resource's own observatory or telescope. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `Switch`, `Receiver`, `Guider`. |
| <span id="spectriggerswhen--name"></span>`name` | string | no | The name of the resource, in this namespace. A device kind needs a name. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spectriggerswhen--type"></span>`type` | string | yes | The condition's type, such as `Safe` or `Parked`. |
| <span id="spectriggerswhen--status"></span>`status` | string | no | The status that the condition must have: `True`, `False`, or `Unknown`. The default is `True`. One of: `True`, `False`, `Unknown`. Default: `True`. |
| <span id="spectriggerswhen--for"></span>`for` | string | no | How long the status must hold before the actions run, as a duration such as `20m`. A change of the status before then cancels the run. Optional. |

#### spec.triggers[].run[]

One action, which names exactly one of `cool`, `warm`, and `job`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectriggersrun--cool"></span>`cool` | [object](#spectriggersruncool) | no | Cools the sensor: the operator writes `celsius` to `CCD_TEMPERATURE` and waits until the sensor is within `within` of it. The default timeout is 20 minutes. |
| <span id="spectriggersrun--warm"></span>`warm` | [object](#spectriggersrunwarm) | no | Warms the sensor before its cooler goes off: the operator writes `celsius` to `CCD_TEMPERATURE`, waits until the sensor is within `within` of it or until the timeout, and switches `CCD_COOLER` off. A cooler cannot warm a sensor above the air around it, so the timeout ends the wait and does not fail the action. The default timeout is 10 minutes. |
| <span id="spectriggersrun--job"></span>`job` | [object](#spectriggersrunjob) | no | A container that the operator runs once as a Kubernetes `Job` in the resource's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes. |
| <span id="spectriggersrun--timeout"></span>`timeout` | string | no | How long the action may take, its waits for `requires` and `after` included, as a duration such as `20m`. An action that passes it fails. Optional. Each action has a default. |
| <span id="spectriggersrun--requires"></span>`requires` | [\[\]object](#spectriggersrunrequires) | no | The conditions that must hold before the action runs. The operator waits for each one until the action's timeout. They bind only the actions that the operator runs: a move that a person makes in KStars is stopped only by the drivers' park locks. |
| <span id="spectriggersrun--after"></span>`after` | [\[\]object](#spectriggersrunafter) | no | The resources whose runs for the same event must end before the action runs: the same lifecycle step, or the same transition of the same condition. A resource with no such run is not waited for. A resource in a later tier of the tree is waited for until the action's timeout, and the action then fails. |

#### spec.triggers[].run[].cool

Cools the sensor: the operator writes `celsius` to `CCD_TEMPERATURE` and waits until the sensor is within `within` of it. The default timeout is 20 minutes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectriggersruncool--celsius"></span>`celsius` | number | yes | The setpoint, in degrees Celsius. |
| <span id="spectriggersruncool--within"></span>`within` | number | no | How close to the setpoint the sensor must come, in degrees Celsius. The default is 0.5. Default: `0.5`. |

#### spec.triggers[].run[].warm

Warms the sensor before its cooler goes off: the operator writes `celsius` to `CCD_TEMPERATURE`, waits until the sensor is within `within` of it or until the timeout, and switches `CCD_COOLER` off. A cooler cannot warm a sensor above the air around it, so the timeout ends the wait and does not fail the action. The default timeout is 10 minutes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectriggersrunwarm--celsius"></span>`celsius` | number | yes | The setpoint, in degrees Celsius. |
| <span id="spectriggersrunwarm--within"></span>`within` | number | no | How close to the setpoint the sensor must come, in degrees Celsius. The default is 0.5. Default: `0.5`. |

#### spec.triggers[].run[].job

A container that the operator runs once as a Kubernetes `Job` in the resource's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectriggersrunjob--image"></span>`image` | string | yes | The container image, used as written, such as `busybox:1.37`. |
| <span id="spectriggersrunjob--command"></span>`command` | []string | no | The command that replaces the image's entrypoint, as a container's `command` does. Optional. |
| <span id="spectriggersrunjob--args"></span>`args` | []string | no | The arguments of the command, as a container's `args` are. Optional. |
| <span id="spectriggersrunjob--env"></span>`env` | [\[\]object](#spectriggersrunjobenv) | no | Variables that the operator adds to the container's environment before its own, as `{name, value}`. A variable that has the name of one of the operator's own is replaced by the operator's. Optional. |

#### spec.triggers[].run[].job.env[]

One variable.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectriggersrunjobenv--name"></span>`name` | string | yes | The variable's name, such as `RELAY_URL`. Pattern: `^[-._a-zA-Z][-._a-zA-Z0-9]*$`. |
| <span id="spectriggersrunjobenv--value"></span>`value` | string | yes | The variable's value. It can be empty. |

#### spec.triggers[].run[].requires[]

One condition, and the status it must have.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectriggersrunrequires--kind"></span>`kind` | string | no | The kind of the resource whose condition it reads. With no `kind`, the condition is the resource's own. A `kind` of `Observatory` or `Telescope` with no `name` means the resource's own observatory or telescope. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `Switch`, `Receiver`, `Guider`. |
| <span id="spectriggersrunrequires--name"></span>`name` | string | no | The name of the resource, in this namespace. A device kind needs a name. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spectriggersrunrequires--type"></span>`type` | string | yes | The condition's type, such as `Safe` or `Parked`. |
| <span id="spectriggersrunrequires--status"></span>`status` | string | no | The status that the condition must have: `True`, `False`, or `Unknown`. The default is `True`. One of: `True`, `False`, `Unknown`. Default: `True`. |

#### spec.triggers[].run[].after[]

One resource, by its kind and name.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectriggersrunafter--kind"></span>`kind` | string | yes | The kind of the resource. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `Switch`, `Receiver`, `Guider`. |
| <span id="spectriggersrunafter--name"></span>`name` | string | no | The name of the resource. With no `name`, the reference names every resource of the kind in the observatory, or the resource's own `Observatory` or `Telescope`. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |

## status

What the operator observes. The operator writes it, and a person reads it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the operator last acted on. |
| <span id="status--phase"></span>`phase` | string | no | The device's state in one word. `Inventory`: the device is on the shelf, with no parent, and the operator creates nothing for it. `Idle`: the device is installed, but no reservation of its telescope or its observatory is active, so it has no pod. `Starting`: a reservation's activation began, or the device's pod starts. `Connecting`: the operator asked the driver to connect. `Connected`: the driver reports the device connected. `Disconnecting`: the operator asked the driver to disconnect, or stops the pod. `Error`: a step failed, and the `Ready` condition gives the reason. One of: `Inventory`, `Idle`, `Starting`, `Connecting`, `Connected`, `Disconnecting`, `Error`. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Ready` is `True` while the device is connected. `ParentFound` is `False` when a resource that the spec names, up to the `Observatory`, does not exist, and its message names the missing resource. A device on the shelf names no parent, and its `ParentFound` is `True` with the reason `NoParent`. While a device is connected, it also reports the state that the triggers of procedures read, by kind: a `Dome` `Parked` and `Open` (the shutter), a `Mount` `Parked`, a `DustCap` `Open`, a `FlatPanel` `Lit` (the light), a `Camera` `Cooling` (the cooler), and a `WeatherStation` `Safe`. A park, a shutter, or a cover that moves is `Unknown` with the reason `Moving`. `Safe` is `False` with the reason `Warning` or `Danger`. A device that is not connected has none of them. |
| <span id="status--procedures"></span>`procedures` | [\[\]object](#statusprocedures) | no | The last run of each trigger of the resource's procedures: `activation`, `deactivation`, and `triggers[0]` and so on for each item of `spec.triggers`. An operator that restarts resumes a run from this record. |
| <span id="status--indidevice"></span>`indiDevice` | string | no | The name that the driver gives its device on the INDI server, such as `CCD Simulator`. KStars shows this name. A device that another resource serves names that resource's device. |
| <span id="status--endpoint"></span>`endpoint` | [object](#statusendpoint) | no | The INDI server that runs the device, while the device is installed. A client such as KStars reaches the device at this address by the name in `indiDevice`. |
| <span id="status--driver"></span>`driver` | string | no | The driver that the device's pod runs. A device that another resource serves has no pod of its own. |
| <span id="status--image"></span>`image` | string | no | The image that the device's pod runs, after the operator resolved it. |
| <span id="status--pod"></span>`pod` | string | no | The name of the device's pod, while it exists. |
| <span id="status--node"></span>`node` | string | no | The node that runs the device's pod. |
| <span id="status--properties"></span>`properties` | [\[\]object](#statusproperties) | no | Every property that the device defines, as the driver defines it, with the values of its last update. A vendor's own properties are here, and no typed reading depends on one. The list holds no BLOB data. |
| <span id="status--activedevices"></span>`activeDevices` | [object](#statusactivedevices) | no | The INDI devices whose state the camera writes into the FITS header of each frame. The status holds what the driver reports in `ACTIVE_DEVICES`. The operator decides it from the resources around the device, so it is not in spec. |
| <span id="status--exposure"></span>`exposure` | [object](#statusexposure) | no | The length of an exposure of the sensor, in seconds. While the exposure runs, the driver counts it down. The status holds what the driver reports in `CCD_EXPOSURE`. Clients such as KStars and Ekos change it. |
| <span id="status--abortexposure"></span>`abortExposure` | [object](#statusabortexposure) | no | Stops the exposure of the sensor that runs. The status holds what the driver reports in `CCD_ABORT_EXPOSURE`. Clients such as KStars and Ekos change it. |
| <span id="status--frame"></span>`frame` | [object](#statusframe) | no | The area of the sensor that the next frame reads, in pixels. The status holds what the driver reports in `CCD_FRAME`. Clients such as KStars and Ekos change it. |
| <span id="status--framereset"></span>`frameReset` | [object](#statusframereset) | no | Sets `frame` back to the whole sensor and the binning back to 1 by 1. The status holds what the driver reports in `CCD_FRAME_RESET`. Clients such as KStars and Ekos change it. |
| <span id="status--binning"></span>`binning` | [object](#statusbinning) | no | How many pixels of the sensor each pixel of the next frame sums, across and down. The status holds what the driver reports in `CCD_BINNING`. Clients such as KStars and Ekos change it. |
| <span id="status--fitsheader"></span>`fitsHeader` | [object](#statusfitsheader) | no | One keyword that the driver adds to the FITS header of each frame after it. The status holds what the driver reports in `FITS_HEADER`. Clients such as KStars and Ekos change it. |
| <span id="status--guiderexposure"></span>`guiderExposure` | [object](#statusguiderexposure) | no | The length of an exposure of the guide head, in seconds. While the exposure runs, the driver counts it down. The status holds what the driver reports in `GUIDER_EXPOSURE`. Clients such as KStars and Ekos change it. |
| <span id="status--guiderabortexposure"></span>`guiderAbortExposure` | [object](#statusguiderabortexposure) | no | Stops the exposure of the guide head that runs. The status holds what the driver reports in `GUIDER_ABORT_EXPOSURE`. Clients such as KStars and Ekos change it. |
| <span id="status--guiderframe"></span>`guiderFrame` | [object](#statusguiderframe) | no | The area of the guide head that its next frame reads, in pixels. The status holds what the driver reports in `GUIDER_FRAME`. Clients such as KStars and Ekos change it. |
| <span id="status--temperature"></span>`temperature` | [object](#statustemperature) | no | The temperature of the sensor, in degrees Celsius. A client that writes it sets the cooler's setpoint. The status holds what the driver reports in `CCD_TEMPERATURE`. Clients such as KStars and Ekos change it. |
| <span id="status--temperatureramp"></span>`temperatureRamp` | [object](#statustemperatureramp) | no | How fast the driver changes the cooler's setpoint toward a new one. The status holds what the driver reports in `CCD_TEMP_RAMP`. `spec.temperatureRamp` sets it. |
| <span id="status--captureformat"></span>`captureFormat` | [object](#statuscaptureformat) | no | The format that the sensor captures in, such as mono or a raw Bayer pattern. The status holds what the driver reports in `CCD_CAPTURE_FORMAT`. `spec.captureFormat` sets it. |
| <span id="status--transferformat"></span>`transferFormat` | [object](#statustransferformat) | no | The format that the driver sends each frame in: FITS, XISF, or the camera's native format. The status holds what the driver reports in `CCD_TRANSFER_FORMAT`. `spec.transferFormat` sets it. |
| <span id="status--info"></span>`info` | [object](#statusinfo) | no | The size of the sensor and of its pixels, and the depth of each pixel. The status holds what the driver reports in `CCD_INFO`. |
| <span id="status--guiderinfo"></span>`guiderInfo` | [object](#statusguiderinfo) | no | The size of the guide head and of its pixels, and the depth of each pixel. The status holds what the driver reports in `GUIDER_INFO`. |
| <span id="status--guiderbinning"></span>`guiderBinning` | [object](#statusguiderbinning) | no | How many pixels of the guide head each pixel of its next frame sums, across and down. The status holds what the driver reports in `GUIDER_BINNING`. Clients such as KStars and Ekos change it. |
| <span id="status--compression"></span>`compression` | [object](#statuscompression) | no | Whether the driver compresses each frame of the sensor before it sends it. The status holds what the driver reports in `CCD_COMPRESSION`. `spec.compression` sets it. |
| <span id="status--imagedata"></span>`imageData` | [object](#statusimagedata) | no | The last frame of the sensor. The operator never asks for its data, so the status holds its state alone. The status holds what the driver reports in `CCD1`. |
| <span id="status--guidercompression"></span>`guiderCompression` | [object](#statusguidercompression) | no | Whether the driver compresses each frame of the guide head before it sends it. The status holds what the driver reports in `GUIDER_COMPRESSION`. `spec.guiderCompression` sets it. |
| <span id="status--guiderimagedata"></span>`guiderImageData` | [object](#statusguiderimagedata) | no | The last frame of the guide head. The operator never asks for its data, so the status holds its state alone. The status holds what the driver reports in `CCD2`. |
| <span id="status--guidenorthsouth"></span>`guideNorthSouth` | [object](#statusguidenorthsouth) | no | A guide pulse to the north or to the south through the camera's ST-4 port, in milliseconds. The status holds what the driver reports in `TELESCOPE_TIMED_GUIDE_NS`. Clients such as KStars and Ekos change it. |
| <span id="status--guidewesteast"></span>`guideWestEast` | [object](#statusguidewesteast) | no | A guide pulse to the west or to the east through the camera's ST-4 port, in milliseconds. The status holds what the driver reports in `TELESCOPE_TIMED_GUIDE_WE`. Clients such as KStars and Ekos change it. |
| <span id="status--frametype"></span>`frameType` | [object](#statusframetype) | no | The type of the sensor's next frame: light, bias, dark, or flat. The status holds what the driver reports in `CCD_FRAME_TYPE`. Clients such as KStars and Ekos change it. |
| <span id="status--guiderframetype"></span>`guiderFrameType` | [object](#statusguiderframetype) | no | The type of the guide head's next frame: light, bias, dark, or flat. The status holds what the driver reports in `GUIDER_FRAME_TYPE`. Clients such as KStars and Ekos change it. |
| <span id="status--cfa"></span>`cfa` | [object](#statuscfa) | no | The color filter array of a color sensor, which a client needs to debayer each frame. The status holds what the driver reports in `CCD_CFA`. |
| <span id="status--scopeinfo"></span>`scopeInfo` | [object](#statusscopeinfo) | no | The optics in front of the camera, which the driver writes into the FITS header of each frame. The operator writes it from the train's `OpticalTube`. The status holds what the driver reports in `SCOPE_INFO`. The operator decides it from the resources around the device, so it is not in spec. |
| <span id="status--wcs"></span>`wcs` | [object](#statuswcs) | no | Whether the driver writes the world coordinate system keywords into the FITS header of each frame. The status holds what the driver reports in `WCS_CONTROL`. `spec.wcs` sets it. |
| <span id="status--uploadmode"></span>`uploadMode` | [object](#statusuploadmode) | no | Where the driver sends each frame: to the client, to a local file, or to both. The status holds what the driver reports in `UPLOAD_MODE`. Clients such as KStars and Ekos change it. |
| <span id="status--uploadsettings"></span>`uploadSettings` | [object](#statusuploadsettings) | no | Where the driver saves a frame that it saves to a local file. The status holds what the driver reports in `UPLOAD_SETTINGS`. Clients such as KStars and Ekos change it. |
| <span id="status--fastexposure"></span>`fastExposure` | [object](#statusfastexposure) | no | Whether the camera starts the next exposure as soon as it reads the last one, for the count in `fastCount`. The status holds what the driver reports in `CCD_FAST_TOGGLE`. Clients such as KStars and Ekos change it. |
| <span id="status--fastcount"></span>`fastCount` | [object](#statusfastcount) | no | How many frames a fast exposure takes, in frames. The driver counts it down. The status holds what the driver reports in `CCD_FAST_COUNT`. Clients such as KStars and Ekos change it. |
| <span id="status--videostream"></span>`videoStream` | [object](#statusvideostream) | no | Turns the video stream of the sensor on or off. The status holds what the driver reports in `CCD_VIDEO_STREAM`. Clients such as KStars and Ekos change it. |
| <span id="status--streamdepth"></span>`streamDepth` | [object](#statusstreamdepth) | no | The depth of each pixel of the video stream, 8 or 16 bits. The status holds what the driver reports in `STREAM_FULL_DEPTH`. Clients such as KStars and Ekos change it. |
| <span id="status--streamdelay"></span>`streamDelay` | [object](#statusstreamdelay) | no | The delay between the frames of the video stream, in seconds. The status holds what the driver reports in `STREAM_DELAY`. |
| <span id="status--streamingexposure"></span>`streamingExposure` | [object](#statusstreamingexposure) | no | The exposure of each frame of the video stream. The status holds what the driver reports in `STREAMING_EXPOSURE`. Clients such as KStars and Ekos change it. |
| <span id="status--fps"></span>`fps` | [object](#statusfps) | no | The frame rate of the video stream, in frames per second. The status holds what the driver reports in `FPS`. |
| <span id="status--recordstream"></span>`recordStream` | [object](#statusrecordstream) | no | Starts or stops a recording of the video stream, with no end, for the time, or for the frames in `recordOptions`. The status holds what the driver reports in `RECORD_STREAM`. Clients such as KStars and Ekos change it. |
| <span id="status--recordfile"></span>`recordFile` | [object](#statusrecordfile) | no | The file that a recording of the video stream writes. The status holds what the driver reports in `RECORD_FILE`. Clients such as KStars and Ekos change it. |
| <span id="status--recordoptions"></span>`recordOptions` | [object](#statusrecordoptions) | no | When a recording of the video stream ends. The status holds what the driver reports in `RECORD_OPTIONS`. Clients such as KStars and Ekos change it. |
| <span id="status--streamframe"></span>`streamFrame` | [object](#statusstreamframe) | no | The area of the sensor that the video stream reads, in pixels. The status holds what the driver reports in `CCD_STREAM_FRAME`. Clients such as KStars and Ekos change it. |
| <span id="status--streamencoder"></span>`streamEncoder` | [object](#statusstreamencoder) | no | The encoder of the video stream, raw or MJPEG. The status holds what the driver reports in `CCD_STREAM_ENCODER`. Clients such as KStars and Ekos change it. |
| <span id="status--streamrecorder"></span>`streamRecorder` | [object](#statusstreamrecorder) | no | The format of a recording of the video stream. The status holds what the driver reports in `CCD_STREAM_RECORDER`. Clients such as KStars and Ekos change it. |
| <span id="status--streamlimits"></span>`streamLimits` | [object](#statusstreamlimits) | no | The limits of the video stream. The status holds what the driver reports in `LIMITS`. Clients such as KStars and Ekos change it. |
| <span id="status--dspconvolution"></span>`dspConvolution` | [object](#statusdspconvolution) | no | Turns the convolution of each frame on or off. The status holds what the driver reports in `DSP_ACTIVATE_CONVOLUTION`. Clients such as KStars and Ekos change it. |
| <span id="status--dspdft"></span>`dspDft` | [object](#statusdspdft) | no | Turns the discrete Fourier transform of each frame on or off. The status holds what the driver reports in `DSP_ACTIVATE_DFT`. Clients such as KStars and Ekos change it. |
| <span id="status--dspidft"></span>`dspIdft` | [object](#statusdspidft) | no | Turns the inverse discrete Fourier transform of each frame on or off. The status holds what the driver reports in `DSP_ACTIVATE_IDFT`. Clients such as KStars and Ekos change it. |
| <span id="status--dspspectrum"></span>`dspSpectrum` | [object](#statusdspspectrum) | no | Turns the spectrum of each frame on or off. The status holds what the driver reports in `DSP_ACTIVATE_SPECTRUM`. Clients such as KStars and Ekos change it. |
| <span id="status--dsphistogram"></span>`dspHistogram` | [object](#statusdsphistogram) | no | Turns the histogram of each frame on or off. The status holds what the driver reports in `DSP_ACTIVATE_HISTOGRAM`. Clients such as KStars and Ekos change it. |
| <span id="status--dspwavelets"></span>`dspWavelets` | [object](#statusdspwavelets) | no | Turns the wavelet transform of each frame on or off. The status holds what the driver reports in `DSP_ACTIVATE_WAVELETS`. Clients such as KStars and Ekos change it. |
| <span id="status--gain"></span>`gain` | [object](#statusgain) | no | The sensor's gain, in the driver's own units. The status holds what the driver reports in `CCD_GAIN`. `spec.gain` sets it. |
| <span id="status--offset"></span>`offset` | [object](#statusoffset) | no | The level that the sensor adds to each pixel, in the driver's own units. The status holds what the driver reports in `CCD_OFFSET`. `spec.offset` sets it. |
| <span id="status--cooler"></span>`cooler` | [object](#statuscooler) | no | Turns the sensor's cooler on or off. The `cool` and `warm` actions change it. The status holds what the driver reports in `CCD_COOLER`. Clients such as KStars and Ekos change it. |
| <span id="status--coolerpower"></span>`coolerPower` | [object](#statuscoolerpower) | no | The power that the cooler draws, in percent of its greatest power. The status holds what the driver reports in `CCD_COOLER_POWER`. |
| <span id="status--display"></span>`display` | [object](#statusdisplay) | no | The readings as a person reads them, with their units, for the printer columns. The numbers are in `readings`. |
| <span id="status--readings"></span>`readings` | [object](#statusreadings) | no | What the camera reports, from `CCD_TEMPERATURE`, `CCD_COOLER`, `CCD_COOLER_POWER`, and `CCD_EXPOSURE`. A field is absent until the driver sends it. |

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

### status.procedures[]

The last run of one trigger.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusprocedures--trigger"></span>`trigger` | string | yes | The trigger: `activation`, `deactivation`, or `triggers[0]` for the first item of `spec.triggers`. |
| <span id="statusprocedures--since"></span>`since` | string | no | The transition time of the condition that started the run: the `lastTransitionTime` of `Active` for activation and deactivation. |
| <span id="statusprocedures--state"></span>`state` | string | yes | The run's progress: `Pending`, `Running`, `Done`, `Failed`, or `Skipped` when it had nothing to do. One of: `Pending`, `Running`, `Done`, `Failed`, `Skipped`. |
| <span id="statusprocedures--starttime"></span>`startTime` | string | no | When the run began to run. |
| <span id="statusprocedures--stoptime"></span>`stopTime` | string | no | When the run ended. |
| <span id="statusprocedures--summary"></span>`summary` | string | no | What the run waits for or did. |
| <span id="statusprocedures--actions"></span>`actions` | [\[\]object](#statusproceduresactions) | no | Each action of the run, in order. |

#### status.procedures[].actions[]

One action.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusproceduresactions--action"></span>`action` | string | yes | The action as its YAML reads, such as `state: Parked` or `cool: -10 °C within 0.5 °C`. |
| <span id="statusproceduresactions--state"></span>`state` | string | yes | The action's progress: `Pending`, `Running`, `Done`, `Failed`, or `Skipped` when it had nothing to do. One of: `Pending`, `Running`, `Done`, `Failed`, `Skipped`. |
| <span id="statusproceduresactions--starttime"></span>`startTime` | string | no | When the action began to run. |
| <span id="statusproceduresactions--stoptime"></span>`stopTime` | string | no | When the action ended. |
| <span id="statusproceduresactions--summary"></span>`summary` | string | no | What the action waits for or did. |

### status.endpoint

The INDI server that runs the device, while the device is installed. A client such as KStars reaches the device at this address by the name in `indiDevice`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusendpoint--service"></span>`service` | string | yes | The name of the server's `Service`. |
| <span id="statusendpoint--host"></span>`host` | string | yes | The DNS name of the server's `Service` in the cluster. |
| <span id="statusendpoint--port"></span>`port` | integer | yes | The INDI port, 7624. |

### status.properties[]

One INDI property.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusproperties--name"></span>`name` | string | yes | The property's name, such as `CCD_TEMPERATURE`. |
| <span id="statusproperties--label"></span>`label` | string | no | The label that the driver gives the property. |
| <span id="statusproperties--group"></span>`group` | string | no | The group, or tab, that the driver puts the property in. |
| <span id="statusproperties--type"></span>`type` | string | yes | The type of the members' values. One of: `Number`, `Switch`, `Text`, `Light`, `BLOB`. |
| <span id="statusproperties--permission"></span>`permission` | string | yes | Whether a client can write the property: `ro` read-only, `wo` write-only, or `rw` read and write. One of: `ro`, `wo`, `rw`. |
| <span id="statusproperties--state"></span>`state` | string | yes | The property's state: `Busy` while the driver carries out a change, `Ok` or `Alert` when the change ends. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusproperties--rule"></span>`rule` | string | no | For a switch property, how many members can be on at once. One of: `OneOfMany`, `AtMostOne`, `AnyOfMany`. |
| <span id="statusproperties--members"></span>`members` | [\[\]object](#statuspropertiesmembers) | no | The property's members, in the driver's order. |

#### status.properties[].members[]

One member of the property.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuspropertiesmembers--name"></span>`name` | string | yes | The member's name, such as `CCD_TEMPERATURE_VALUE`. |
| <span id="statuspropertiesmembers--label"></span>`label` | string | no | The label that the driver gives the member. |
| <span id="statuspropertiesmembers--value"></span>`value` | string | no | The value as text: a number in the member's format, `On` or `Off` for a switch, a state for a light, or the text. A BLOB member has no value here. |
| <span id="statuspropertiesmembers--min"></span>`min` | number | no | For a number, the least value that the driver accepts. |
| <span id="statuspropertiesmembers--max"></span>`max` | number | no | For a number, the greatest value that the driver accepts. |
| <span id="statuspropertiesmembers--step"></span>`step` | number | no | For a number, the step between values. Zero means any step. |

### status.activeDevices

The INDI devices whose state the camera writes into the FITS header of each frame. The status holds what the driver reports in `ACTIVE_DEVICES`. The operator decides it from the resources around the device, so it is not in spec.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusactivedevices--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusactivedevices--telescope"></span>`telescope` | string | no | The mount, for the coordinates and the pier side. The member is `ACTIVE_TELESCOPE`. |
| <span id="statusactivedevices--rotator"></span>`rotator` | string | no | The rotator, for the angle of the camera. The member is `ACTIVE_ROTATOR`. |
| <span id="statusactivedevices--focuser"></span>`focuser` | string | no | The focuser, for its position and temperature. The member is `ACTIVE_FOCUSER`. |
| <span id="statusactivedevices--filter"></span>`filter` | string | no | The filter wheel, for the name of the filter. The member is `ACTIVE_FILTER`. |
| <span id="statusactivedevices--skyquality"></span>`skyQuality` | string | no | The sky quality meter, for the brightness of the sky. The member is `ACTIVE_SKYQUALITY`. |

### status.exposure

The length of an exposure of the sensor, in seconds. While the exposure runs, the driver counts it down. The status holds what the driver reports in `CCD_EXPOSURE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusexposure--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusexposure--value"></span>`value` | number | no | The value that the driver reports. The unit is seconds. |

### status.abortExposure

Stops the exposure of the sensor that runs. The status holds what the driver reports in `CCD_ABORT_EXPOSURE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusabortexposure--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusabortexposure--value"></span>`value` | string | no | The name of the member that is on, such as `ABORT`. Absent when no member is on. |

### status.frame

The area of the sensor that the next frame reads, in pixels. The status holds what the driver reports in `CCD_FRAME`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusframe--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusframe--x"></span>`x` | number | no | The left edge of the area, in pixels. The member is `X`. |
| <span id="statusframe--y"></span>`y` | number | no | The top edge of the area, in pixels. The member is `Y`. |
| <span id="statusframe--width"></span>`width` | number | no | The width of the area, in pixels. The member is `WIDTH`. |
| <span id="statusframe--height"></span>`height` | number | no | The height of the area, in pixels. The member is `HEIGHT`. |

### status.frameReset

Sets `frame` back to the whole sensor and the binning back to 1 by 1. The status holds what the driver reports in `CCD_FRAME_RESET`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusframereset--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusframereset--value"></span>`value` | string | no | The name of the member that is on, such as `RESET`. Absent when no member is on. |

### status.binning

How many pixels of the sensor each pixel of the next frame sums, across and down. The status holds what the driver reports in `CCD_BINNING`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusbinning--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusbinning--horizontal"></span>`horizontal` | number | no | The binning across, such as 2. The member is `HOR_BIN`. |
| <span id="statusbinning--vertical"></span>`vertical` | number | no | The binning down, such as 2. The member is `VER_BIN`. |

### status.fitsHeader

One keyword that the driver adds to the FITS header of each frame after it. The status holds what the driver reports in `FITS_HEADER`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusfitsheader--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusfitsheader--name"></span>`name` | string | no | The keyword, such as `OBSERVER`. The member is `KEYWORD_NAME`. |
| <span id="statusfitsheader--value"></span>`value` | string | no | The value of the keyword. The member is `KEYWORD_VALUE`. |
| <span id="statusfitsheader--comment"></span>`comment` | string | no | The comment of the keyword. The member is `KEYWORD_COMMENT`. |

### status.guiderExposure

The length of an exposure of the guide head, in seconds. While the exposure runs, the driver counts it down. The status holds what the driver reports in `GUIDER_EXPOSURE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusguiderexposure--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusguiderexposure--value"></span>`value` | number | no | The value that the driver reports. The unit is seconds. |

### status.guiderAbortExposure

Stops the exposure of the guide head that runs. The status holds what the driver reports in `GUIDER_ABORT_EXPOSURE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusguiderabortexposure--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusguiderabortexposure--value"></span>`value` | string | no | The name of the member that is on, such as `ABORT`. Absent when no member is on. |

### status.guiderFrame

The area of the guide head that its next frame reads, in pixels. The status holds what the driver reports in `GUIDER_FRAME`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusguiderframe--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusguiderframe--x"></span>`x` | number | no | The left edge of the area, in pixels. The member is `X`. |
| <span id="statusguiderframe--y"></span>`y` | number | no | The top edge of the area, in pixels. The member is `Y`. |
| <span id="statusguiderframe--width"></span>`width` | number | no | The width of the area, in pixels. The member is `WIDTH`. |
| <span id="statusguiderframe--height"></span>`height` | number | no | The height of the area, in pixels. The member is `HEIGHT`. |

### status.temperature

The temperature of the sensor, in degrees Celsius. A client that writes it sets the cooler's setpoint. The status holds what the driver reports in `CCD_TEMPERATURE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statustemperature--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statustemperature--value"></span>`value` | number | no | The value that the driver reports. The unit is degrees Celsius. |

### status.temperatureRamp

How fast the driver changes the cooler's setpoint toward a new one. The status holds what the driver reports in `CCD_TEMP_RAMP`. `spec.temperatureRamp` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statustemperatureramp--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statustemperatureramp--slope"></span>`slope` | number | no | The greatest change of the setpoint, in degrees Celsius per minute. Zero turns the ramp off. The member is `RAMP_SLOPE`. |
| <span id="statustemperatureramp--threshold"></span>`threshold` | number | no | How close to the setpoint the sensor must come before the change is done, in degrees Celsius. The member is `RAMP_THRESHOLD`. |

### status.captureFormat

The format that the sensor captures in, such as mono or a raw Bayer pattern. The status holds what the driver reports in `CCD_CAPTURE_FORMAT`. `spec.captureFormat` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscaptureformat--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statuscaptureformat--elements"></span>`elements` | [\[\]object](#statuscaptureformatelements) | no | Each element that the driver defines, in the driver's order. |

#### status.captureFormat.elements[]

One element that the driver defines.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscaptureformatelements--name"></span>`name` | string | yes | The element's name, as the driver defines it. |
| <span id="statuscaptureformatelements--label"></span>`label` | string | no | The label that the driver gives the element. |
| <span id="statuscaptureformatelements--active"></span>`active` | boolean | no | Whether the element is on. |

### status.transferFormat

The format that the driver sends each frame in: FITS, XISF, or the camera's native format. The status holds what the driver reports in `CCD_TRANSFER_FORMAT`. `spec.transferFormat` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statustransferformat--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statustransferformat--value"></span>`value` | string | no | The name of the member that is on, such as `FORMAT_FITS`. Absent when no member is on. |

### status.info

The size of the sensor and of its pixels, and the depth of each pixel. The status holds what the driver reports in `CCD_INFO`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusinfo--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusinfo--maxx"></span>`maxX` | number | no | The width of the sensor, in pixels. The member is `CCD_MAX_X`. |
| <span id="statusinfo--maxy"></span>`maxY` | number | no | The height of the sensor, in pixels. The member is `CCD_MAX_Y`. |
| <span id="statusinfo--pixelsize"></span>`pixelSize` | number | no | The size of one pixel, in micrometers. The member is `CCD_PIXEL_SIZE`. |
| <span id="statusinfo--pixelsizex"></span>`pixelSizeX` | number | no | The width of one pixel, in micrometers. The member is `CCD_PIXEL_SIZE_X`. |
| <span id="statusinfo--pixelsizey"></span>`pixelSizeY` | number | no | The height of one pixel, in micrometers. The member is `CCD_PIXEL_SIZE_Y`. |
| <span id="statusinfo--bitsperpixel"></span>`bitsPerPixel` | number | no | The depth of one pixel, in bits. The member is `CCD_BITSPERPIXEL`. |

### status.guiderInfo

The size of the guide head and of its pixels, and the depth of each pixel. The status holds what the driver reports in `GUIDER_INFO`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusguiderinfo--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusguiderinfo--maxx"></span>`maxX` | number | no | The width of the guide head, in pixels. The member is `CCD_MAX_X`. |
| <span id="statusguiderinfo--maxy"></span>`maxY` | number | no | The height of the guide head, in pixels. The member is `CCD_MAX_Y`. |
| <span id="statusguiderinfo--pixelsize"></span>`pixelSize` | number | no | The size of one pixel, in micrometers. The member is `CCD_PIXEL_SIZE`. |
| <span id="statusguiderinfo--pixelsizex"></span>`pixelSizeX` | number | no | The width of one pixel, in micrometers. The member is `CCD_PIXEL_SIZE_X`. |
| <span id="statusguiderinfo--pixelsizey"></span>`pixelSizeY` | number | no | The height of one pixel, in micrometers. The member is `CCD_PIXEL_SIZE_Y`. |
| <span id="statusguiderinfo--bitsperpixel"></span>`bitsPerPixel` | number | no | The depth of one pixel, in bits. The member is `CCD_BITSPERPIXEL`. |

### status.guiderBinning

How many pixels of the guide head each pixel of its next frame sums, across and down. The status holds what the driver reports in `GUIDER_BINNING`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusguiderbinning--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusguiderbinning--horizontal"></span>`horizontal` | number | no | The binning across, such as 2. The member is `HOR_BIN`. |
| <span id="statusguiderbinning--vertical"></span>`vertical` | number | no | The binning down, such as 2. The member is `VER_BIN`. |

### status.compression

Whether the driver compresses each frame of the sensor before it sends it. The status holds what the driver reports in `CCD_COMPRESSION`. `spec.compression` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscompression--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statuscompression--value"></span>`value` | string | no | The name of the member that is on, such as `INDI_ENABLED`. Absent when no member is on. |

### status.imageData

The last frame of the sensor. The operator never asks for its data, so the status holds its state alone. The status holds what the driver reports in `CCD1`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusimagedata--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |

### status.guiderCompression

Whether the driver compresses each frame of the guide head before it sends it. The status holds what the driver reports in `GUIDER_COMPRESSION`. `spec.guiderCompression` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusguidercompression--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusguidercompression--value"></span>`value` | string | no | The name of the member that is on, such as `INDI_ENABLED`. Absent when no member is on. |

### status.guiderImageData

The last frame of the guide head. The operator never asks for its data, so the status holds its state alone. The status holds what the driver reports in `CCD2`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusguiderimagedata--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |

### status.guideNorthSouth

A guide pulse to the north or to the south through the camera's ST-4 port, in milliseconds. The status holds what the driver reports in `TELESCOPE_TIMED_GUIDE_NS`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusguidenorthsouth--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusguidenorthsouth--north"></span>`north` | number | no | The length of a pulse to the north, in milliseconds. The member is `TIMED_GUIDE_N`. |
| <span id="statusguidenorthsouth--south"></span>`south` | number | no | The length of a pulse to the south, in milliseconds. The member is `TIMED_GUIDE_S`. |

### status.guideWestEast

A guide pulse to the west or to the east through the camera's ST-4 port, in milliseconds. The status holds what the driver reports in `TELESCOPE_TIMED_GUIDE_WE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusguidewesteast--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusguidewesteast--west"></span>`west` | number | no | The length of a pulse to the west, in milliseconds. The member is `TIMED_GUIDE_W`. |
| <span id="statusguidewesteast--east"></span>`east` | number | no | The length of a pulse to the east, in milliseconds. The member is `TIMED_GUIDE_E`. |

### status.frameType

The type of the sensor's next frame: light, bias, dark, or flat. The status holds what the driver reports in `CCD_FRAME_TYPE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusframetype--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusframetype--value"></span>`value` | string | no | The name of the member that is on, such as `FRAME_LIGHT`. Absent when no member is on. |

### status.guiderFrameType

The type of the guide head's next frame: light, bias, dark, or flat. The status holds what the driver reports in `GUIDER_FRAME_TYPE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusguiderframetype--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusguiderframetype--value"></span>`value` | string | no | The name of the member that is on, such as `FRAME_LIGHT`. Absent when no member is on. |

### status.cfa

The color filter array of a color sensor, which a client needs to debayer each frame. The status holds what the driver reports in `CCD_CFA`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscfa--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statuscfa--offsetx"></span>`offsetX` | string | no | The column where the pattern starts. The member is `CFA_OFFSET_X`. |
| <span id="statuscfa--offsety"></span>`offsetY` | string | no | The row where the pattern starts. The member is `CFA_OFFSET_Y`. |
| <span id="statuscfa--type"></span>`type` | string | no | The pattern, such as `RGGB`. The member is `CFA_TYPE`. |

### status.scopeInfo

The optics in front of the camera, which the driver writes into the FITS header of each frame. The operator writes it from the train's `OpticalTube`. The status holds what the driver reports in `SCOPE_INFO`. The operator decides it from the resources around the device, so it is not in spec.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusscopeinfo--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusscopeinfo--focallength"></span>`focalLength` | number | no | The focal length, in millimeters. The member is `FOCAL_LENGTH`. |
| <span id="statusscopeinfo--aperture"></span>`aperture` | number | no | The aperture, in millimeters. The member is `APERTURE`. |

### status.wcs

Whether the driver writes the world coordinate system keywords into the FITS header of each frame. The status holds what the driver reports in `WCS_CONTROL`. `spec.wcs` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuswcs--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statuswcs--value"></span>`value` | string | no | The name of the member that is on, such as `WCS_ENABLE`. Absent when no member is on. |

### status.uploadMode

Where the driver sends each frame: to the client, to a local file, or to both. The status holds what the driver reports in `UPLOAD_MODE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusuploadmode--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusuploadmode--value"></span>`value` | string | no | The name of the member that is on, such as `UPLOAD_CLIENT`. Absent when no member is on. |

### status.uploadSettings

Where the driver saves a frame that it saves to a local file. The status holds what the driver reports in `UPLOAD_SETTINGS`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusuploadsettings--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusuploadsettings--directory"></span>`directory` | string | no | The directory, in the driver's container. The member is `UPLOAD_DIR`. |
| <span id="statusuploadsettings--prefix"></span>`prefix` | string | no | The start of each file's name. The member is `UPLOAD_PREFIX`. |

### status.fastExposure

Whether the camera starts the next exposure as soon as it reads the last one, for the count in `fastCount`. The status holds what the driver reports in `CCD_FAST_TOGGLE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusfastexposure--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusfastexposure--value"></span>`value` | string | no | The name of the member that is on, such as `INDI_ENABLED`. Absent when no member is on. |

### status.fastCount

How many frames a fast exposure takes, in frames. The driver counts it down. The status holds what the driver reports in `CCD_FAST_COUNT`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusfastcount--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusfastcount--value"></span>`value` | number | no | The value that the driver reports. The unit is frames. |

### status.videoStream

Turns the video stream of the sensor on or off. The status holds what the driver reports in `CCD_VIDEO_STREAM`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusvideostream--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusvideostream--value"></span>`value` | string | no | The name of the member that is on, such as `STREAM_ON`. Absent when no member is on. |

### status.streamDepth

The depth of each pixel of the video stream, 8 or 16 bits. The status holds what the driver reports in `STREAM_FULL_DEPTH`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstreamdepth--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusstreamdepth--value"></span>`value` | string | no | The name of the member that is on, such as `FULL_DEPTH_8BIT`. Absent when no member is on. |

### status.streamDelay

The delay between the frames of the video stream, in seconds. The status holds what the driver reports in `STREAM_DELAY`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstreamdelay--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusstreamdelay--value"></span>`value` | number | no | The value that the driver reports. The unit is seconds. |

### status.streamingExposure

The exposure of each frame of the video stream. The status holds what the driver reports in `STREAMING_EXPOSURE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstreamingexposure--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusstreamingexposure--duration"></span>`duration` | number | no | The length of each exposure, in seconds. Some cameras do not control it. The member is `STREAMING_EXPOSURE_VALUE`. |
| <span id="statusstreamingexposure--divisor"></span>`divisor` | number | no | The stream sends one frame in this many. 1 sends every frame. The member is `STREAMING_DIVISOR_VALUE`. |

### status.fps

The frame rate of the video stream, in frames per second. The status holds what the driver reports in `FPS`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusfps--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusfps--instant"></span>`instant` | number | no | The rate of the last frames, in frames per second. The member is `EST_FPS`. |
| <span id="statusfps--average"></span>`average` | number | no | The average rate over one second, in frames per second. The member is `AVG_FPS`. |

### status.recordStream

Starts or stops a recording of the video stream, with no end, for the time, or for the frames in `recordOptions`. The status holds what the driver reports in `RECORD_STREAM`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusrecordstream--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusrecordstream--value"></span>`value` | string | no | The name of the member that is on, such as `RECORD_ON`. Absent when no member is on. |

### status.recordFile

The file that a recording of the video stream writes. The status holds what the driver reports in `RECORD_FILE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusrecordfile--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusrecordfile--directory"></span>`directory` | string | no | The directory, in the driver's container. The member is `RECORD_FILE_DIR`. |
| <span id="statusrecordfile--name"></span>`name` | string | no | The file's name. The member is `RECORD_FILE_NAME`. |

### status.recordOptions

When a recording of the video stream ends. The status holds what the driver reports in `RECORD_OPTIONS`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusrecordoptions--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusrecordoptions--duration"></span>`duration` | number | no | The length of a recording, in seconds. The member is `RECORD_DURATION`. |
| <span id="statusrecordoptions--frames"></span>`frames` | number | no | The length of a recording, in frames. The member is `RECORD_FRAME_TOTAL`. |

### status.streamFrame

The area of the sensor that the video stream reads, in pixels. The status holds what the driver reports in `CCD_STREAM_FRAME`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstreamframe--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusstreamframe--x"></span>`x` | number | no | The left edge of the area, in pixels. The member is `X`. |
| <span id="statusstreamframe--y"></span>`y` | number | no | The top edge of the area, in pixels. The member is `Y`. |
| <span id="statusstreamframe--width"></span>`width` | number | no | The width of the area, in pixels. The member is `WIDTH`. |
| <span id="statusstreamframe--height"></span>`height` | number | no | The height of the area, in pixels. The member is `HEIGHT`. |

### status.streamEncoder

The encoder of the video stream, raw or MJPEG. The status holds what the driver reports in `CCD_STREAM_ENCODER`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstreamencoder--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusstreamencoder--value"></span>`value` | string | no | The name of the member that is on, such as `RAW`. Absent when no member is on. |

### status.streamRecorder

The format of a recording of the video stream. The status holds what the driver reports in `CCD_STREAM_RECORDER`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstreamrecorder--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusstreamrecorder--value"></span>`value` | string | no | The name of the member that is on, such as `SER`. Absent when no member is on. |

### status.streamLimits

The limits of the video stream. The status holds what the driver reports in `LIMITS`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstreamlimits--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusstreamlimits--buffermax"></span>`bufferMax` | number | no | The largest buffer of frames, in megabytes. The member is `LIMITS_BUFFER_MAX`. |
| <span id="statusstreamlimits--previewfps"></span>`previewFps` | number | no | The greatest rate that the stream sends to a client, in frames per second. The member is `LIMITS_PREVIEW_FPS`. |

### status.dspConvolution

Turns the convolution of each frame on or off. The status holds what the driver reports in `DSP_ACTIVATE_CONVOLUTION`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdspconvolution--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusdspconvolution--value"></span>`value` | string | no | The name of the member that is on, such as `DSP_ACTIVATE_ON`. Absent when no member is on. |

### status.dspDft

Turns the discrete Fourier transform of each frame on or off. The status holds what the driver reports in `DSP_ACTIVATE_DFT`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdspdft--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusdspdft--value"></span>`value` | string | no | The name of the member that is on, such as `DSP_ACTIVATE_ON`. Absent when no member is on. |

### status.dspIdft

Turns the inverse discrete Fourier transform of each frame on or off. The status holds what the driver reports in `DSP_ACTIVATE_IDFT`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdspidft--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusdspidft--value"></span>`value` | string | no | The name of the member that is on, such as `DSP_ACTIVATE_ON`. Absent when no member is on. |

### status.dspSpectrum

Turns the spectrum of each frame on or off. The status holds what the driver reports in `DSP_ACTIVATE_SPECTRUM`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdspspectrum--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusdspspectrum--value"></span>`value` | string | no | The name of the member that is on, such as `DSP_ACTIVATE_ON`. Absent when no member is on. |

### status.dspHistogram

Turns the histogram of each frame on or off. The status holds what the driver reports in `DSP_ACTIVATE_HISTOGRAM`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdsphistogram--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusdsphistogram--value"></span>`value` | string | no | The name of the member that is on, such as `DSP_ACTIVATE_ON`. Absent when no member is on. |

### status.dspWavelets

Turns the wavelet transform of each frame on or off. The status holds what the driver reports in `DSP_ACTIVATE_WAVELETS`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdspwavelets--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusdspwavelets--value"></span>`value` | string | no | The name of the member that is on, such as `DSP_ACTIVATE_ON`. Absent when no member is on. |

### status.gain

The sensor's gain, in the driver's own units. The status holds what the driver reports in `CCD_GAIN`. `spec.gain` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusgain--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusgain--value"></span>`value` | number | no | The value that the driver reports. The unit is the driver's own. |

### status.offset

The level that the sensor adds to each pixel, in the driver's own units. The status holds what the driver reports in `CCD_OFFSET`. `spec.offset` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusoffset--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusoffset--value"></span>`value` | number | no | The value that the driver reports. The unit is the driver's own. |

### status.cooler

Turns the sensor's cooler on or off. The `cool` and `warm` actions change it. The status holds what the driver reports in `CCD_COOLER`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscooler--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statuscooler--value"></span>`value` | string | no | The name of the member that is on, such as `COOLER_ON`. Absent when no member is on. |

### status.coolerPower

The power that the cooler draws, in percent of its greatest power. The status holds what the driver reports in `CCD_COOLER_POWER`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscoolerpower--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statuscoolerpower--value"></span>`value` | number | no | The value that the driver reports. The unit is percent. |

### status.display

The readings as a person reads them, with their units, for the printer columns. The numbers are in `readings`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdisplay--temperature"></span>`temperature` | string | no | The sensor's temperature, such as `-9.8 °C`. |
| <span id="statusdisplay--setpoint"></span>`setpoint` | string | no | The setpoint of the `cool` action in `spec.activation`, such as `-10 °C`. |
| <span id="statusdisplay--cooler"></span>`cooler` | string | no | `On` or `Off`, from `CCD_COOLER`. |
| <span id="statusdisplay--coolerpower"></span>`coolerPower` | string | no | The cooler's power, such as `42 %`. |
| <span id="statusdisplay--exposure"></span>`exposure` | string | no | The time left, such as `12 s`, while an exposure runs, and `readings.exposure` otherwise. |

### status.readings

What the camera reports, from `CCD_TEMPERATURE`, `CCD_COOLER`, `CCD_COOLER_POWER`, and `CCD_EXPOSURE`. A field is absent until the driver sends it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusreadings--temperature"></span>`temperature` | number | no | The sensor's temperature, in degrees Celsius. |
| <span id="statusreadings--cooler"></span>`cooler` | boolean | no | Whether the cooler is on: `true` while the `COOLER_ON` member of `CCD_COOLER` is on. |
| <span id="statusreadings--coolerpower"></span>`coolerPower` | number | no | The cooler's power, in percent. |
| <span id="statusreadings--exposure"></span>`exposure` | string | no | The state of `CCD_EXPOSURE`: `Idle` before the first exposure, `Exposing` while an exposure runs (`Busy`), `Done` when it ended (`Ok`), and `Failed` when it failed (`Alert`). One of: `Idle`, `Exposing`, `Done`, `Failed`. |
| <span id="statusreadings--exposureremaining"></span>`exposureRemaining` | number | no | The time left in the exposure, in seconds. |
