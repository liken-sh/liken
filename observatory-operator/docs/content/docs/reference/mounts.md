---
title: Mount
weight: 125
toc: true
---

<!-- Generated from deploy/mounts-crd.yaml by crdref. Do not edit. -->

# `Mount`

A `Mount` is the device that points a `Telescope`: an INDI device that serves `TELESCOPE_INTERFACE`. INDI and ASCOM call a mount a telescope, and here the `Telescope` is the whole instrument. The operator writes the observatory's location and time to the mount when it configures it.

## spec

One mount, with INDI's `TELESCOPE_INTERFACE`. The operator writes the `Observatory`'s location to it when a reservation configures it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--telescope"></span>`telescope` | string | no | The name of the `Telescope` that the device belongs to. Optional. With no `telescope`, the device is on the shelf, and the operator creates nothing for it. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spec--driver"></span>`driver` | [object](#specdriver) | no | The INDI driver that runs the device. With no `image`, the operator runs the image that the `indi` build lists for the driver, so `name` must then be an INDI driver that the image holds, such as `indi_simulator_ccd`. Set `driver` or `device`, not both. |
| <span id="spec--device"></span>`device` | [object](#specdevice) | no | The resource whose driver serves this device, in place of a driver of its own. INDI puts every interface of one driver on one device, so a camera with a built-in filter wheel serves a `FilterWheel`, and a flat panel with a motorized cover serves a `DustCap`. The resource it names runs the driver, holds the pod, the claim, and the power, and must be on the same INDI server. The operator connects that device before this one's activation, and runs this one's deactivation before that device stops. Set `driver` or `device`, not both. The field cannot change, and a resource that runs a driver cannot gain it: delete the resource and create it again to move it to another device. |
| <span id="spec--power"></span>`power` | [object](#specpower) | no | The `Switch` output that powers the device. Activation switches the output on before the device's pod starts, and deactivation switches it off after the pod stops. Optional. With no power, a person powers the device on and off by hand. |
| <span id="spec--claim"></span>`claim` | object | no | A `ResourceClaimSpec` of the `resource.k8s.io` API, for real hardware. The operator creates a `ResourceClaim` from it for the device's pod. The API server validates it when the operator creates the claim. Optional. A simulator needs no claim. |
| <span id="spec--trackmode"></span>`trackMode` | string | no | The rate that the mount tracks at, such as sidereal, solar, or lunar. The operator writes it to `TELESCOPE_TRACK_MODE` once, in the `Configure` step of each reservation. Optional. It names one of the elements that the driver defines, such as `TRACK_SIDEREAL`. |
| <span id="spec--reversemotion"></span>`reverseMotion` | []string | no | The axes whose manual motion the driver reverses. The operator writes it to `TELESCOPE_REVERSE_MOTION` once, in the `Configure` step of each reservation. Optional. |
| <span id="spec--simulatepierside"></span>`simulatePierSide` | string | no | Whether the driver computes the pier side, for a mount that does not report it. The operator writes it to `SIMULATE_PIER_SIDE` once, in the `Configure` step of each reservation. Optional. One of: `SIMULATE_YES`, `SIMULATE_NO`. |
| <span id="spec--pec"></span>`pec` | string | no | Whether the mount plays back its periodic error correction. The operator writes it to `PEC` once, in the `Configure` step of each reservation. Optional. One of: `PEC OFF`, `PEC ON`. |
| <span id="spec--joystick"></span>`joystick` | string | no | Whether the driver moves the mount from a joystick. The operator writes it to `USEJOYSTICK` once, in the `Configure` step of each reservation. Optional. One of: `ENABLE`, `DISABLE`. |
| <span id="spec--parkposition"></span>`parkPosition` | [\[\]object](#specparkposition) | no | Where the mount parks, in the unit that each element's label names. Its elements depend on the mount's park type, such as right ascension and declination, or azimuth and altitude. The operator writes it to `TELESCOPE_PARK_POSITION` once, in the `Configure` step of each reservation. Optional. It gives the value of each element that it names, such as `PARK_AZ`. |
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

### spec.parkPosition[]

One element, and the value to write to it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specparkposition--name"></span>`name` | string | yes | The element's name, as the driver defines it. |
| <span id="specparkposition--value"></span>`value` | number | yes | The element's value. The unit is the one that the element's label names. |

### spec.activation[]

One action, which names exactly one of `state` and `job`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specactivation--state"></span>`state` | string | no | The park to reach: `Parked` or `Unparked`, written to `TELESCOPE_PARK`. A park that the driver reports already is not sent again. The default timeout is 10 minutes. One of: `Parked`, `Unparked`. |
| <span id="specactivation--job"></span>`job` | [object](#specactivationjob) | no | A container that the operator runs once as a Kubernetes `Job` in the resource's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes. |
| <span id="specactivation--timeout"></span>`timeout` | string | no | How long the action may take, its waits for `requires` and `after` included, as a duration such as `20m`. An action that passes it fails. Optional. Each action has a default. |
| <span id="specactivation--requires"></span>`requires` | [\[\]object](#specactivationrequires) | no | The conditions that must hold before the action runs. The operator waits for each one until the action's timeout. They bind only the actions that the operator runs: a move that a person makes in KStars is stopped only by the drivers' park locks. |
| <span id="specactivation--after"></span>`after` | [\[\]object](#specactivationafter) | no | The resources whose runs for the same event must end before the action runs: the same lifecycle step, or the same transition of the same condition. A resource with no such run is not waited for. A resource in a later tier of the tree is waited for until the action's timeout, and the action then fails. |

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
| <span id="specactivationrequires--status"></span>`status` | string | no | The status that the condition must have: `True`, `False`, or `Unknown`. The default is `True`. One of: `True`, `False`, `Unknown`. |

#### spec.activation[].after[]

One resource, by its kind and name.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specactivationafter--kind"></span>`kind` | string | yes | The kind of the resource. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `Switch`, `Receiver`, `Guider`. |
| <span id="specactivationafter--name"></span>`name` | string | no | The name of the resource. With no `name`, the reference names every resource of the kind in the observatory, or the resource's own `Observatory` or `Telescope`. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |

### spec.deactivation[]

One action, which names exactly one of `state` and `job`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdeactivation--state"></span>`state` | string | no | The park to reach: `Parked` or `Unparked`, written to `TELESCOPE_PARK`. A park that the driver reports already is not sent again. The default timeout is 10 minutes. One of: `Parked`, `Unparked`. |
| <span id="specdeactivation--job"></span>`job` | [object](#specdeactivationjob) | no | A container that the operator runs once as a Kubernetes `Job` in the resource's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes. |
| <span id="specdeactivation--timeout"></span>`timeout` | string | no | How long the action may take, its waits for `requires` and `after` included, as a duration such as `20m`. An action that passes it fails. Optional. Each action has a default. |
| <span id="specdeactivation--requires"></span>`requires` | [\[\]object](#specdeactivationrequires) | no | The conditions that must hold before the action runs. The operator waits for each one until the action's timeout. They bind only the actions that the operator runs: a move that a person makes in KStars is stopped only by the drivers' park locks. |
| <span id="specdeactivation--after"></span>`after` | [\[\]object](#specdeactivationafter) | no | The resources whose runs for the same event must end before the action runs: the same lifecycle step, or the same transition of the same condition. A resource with no such run is not waited for. A resource in a later tier of the tree is waited for until the action's timeout, and the action then fails. |

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
| <span id="specdeactivationrequires--status"></span>`status` | string | no | The status that the condition must have: `True`, `False`, or `Unknown`. The default is `True`. One of: `True`, `False`, `Unknown`. |

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
| <span id="spectriggerswhen--status"></span>`status` | string | no | The status that the condition must have: `True`, `False`, or `Unknown`. The default is `True`. One of: `True`, `False`, `Unknown`. |
| <span id="spectriggerswhen--for"></span>`for` | string | no | How long the status must hold before the actions run, as a duration such as `20m`. A change of the status before then cancels the run. Optional. |

#### spec.triggers[].run[]

One action, which names exactly one of `state` and `job`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectriggersrun--state"></span>`state` | string | no | The park to reach: `Parked` or `Unparked`, written to `TELESCOPE_PARK`. A park that the driver reports already is not sent again. The default timeout is 10 minutes. One of: `Parked`, `Unparked`. |
| <span id="spectriggersrun--job"></span>`job` | [object](#spectriggersrunjob) | no | A container that the operator runs once as a Kubernetes `Job` in the resource's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes. |
| <span id="spectriggersrun--timeout"></span>`timeout` | string | no | How long the action may take, its waits for `requires` and `after` included, as a duration such as `20m`. An action that passes it fails. Optional. Each action has a default. |
| <span id="spectriggersrun--requires"></span>`requires` | [\[\]object](#spectriggersrunrequires) | no | The conditions that must hold before the action runs. The operator waits for each one until the action's timeout. They bind only the actions that the operator runs: a move that a person makes in KStars is stopped only by the drivers' park locks. |
| <span id="spectriggersrun--after"></span>`after` | [\[\]object](#spectriggersrunafter) | no | The resources whose runs for the same event must end before the action runs: the same lifecycle step, or the same transition of the same condition. A resource with no such run is not waited for. A resource in a later tier of the tree is waited for until the action's timeout, and the action then fails. |

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
| <span id="spectriggersrunrequires--status"></span>`status` | string | no | The status that the condition must have: `True`, `False`, or `Unknown`. The default is `True`. One of: `True`, `False`, `Unknown`. |

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
| <span id="status--indidevice"></span>`indiDevice` | string | no | The name that the driver gives its device on the INDI server, such as `CCD Simulator`. KStars shows this name. A device that another resource serves names that resource's device. The field is present only while the driver runs and defines the device on its server, so an `Idle` device and a device on the shelf have none. |
| <span id="status--endpoint"></span>`endpoint` | [object](#statusendpoint) | no | The INDI server that runs the device. A client such as KStars reaches the device at this address by the name in `indiDevice`. The field is present only while the pod of the device's driver runs and is not stopping, which for a device that another resource serves is that resource's pod, and it names the server in that pod's `observatory.liken.sh/server` label. The driver stops on that server before the pod is deleted. A device that moves to another telescope names the server it left until that server deletes its pod. When a reservation ends, or the device moves to the shelf, the operator removes the field. |
| <span id="status--driver"></span>`driver` | string | no | The driver that the device's pod runs. A device that another resource serves has no pod of its own. |
| <span id="status--image"></span>`image` | string | no | The image that the device's pod runs, after the operator resolved it. |
| <span id="status--pod"></span>`pod` | string | no | The name of the device's pod, while it exists. |
| <span id="status--node"></span>`node` | string | no | The node that runs the device's pod, while the pod exists. |
| <span id="status--limits"></span>`limits` | map[string]map[string]object | no | The range that the driver gives each number of this resource's typed fields that a client can write, by INDI property and then by member, such as `limits.CCD_TEMPERATURE.CCD_TEMPERATURE_VALUE`. Each range has `min` and `max`, the least and the greatest value that the driver accepts, and `step` when the driver states one. The schema describes each typed field, and the driver chooses only these ranges, such as a camera's least and greatest temperature or its range of gain. |
| <span id="status--properties"></span>`properties` | [\[\]object](#statusproperties) | no | Each property of the INDI device that no typed field holds, as the driver defines it, with the values of its last update: a vendor's own properties, the properties that every INDI device has, such as `DEBUG`, and the standard properties of an interface that no resource on the device is, such as the `FILTER_SLOT` of a camera with no `FilterWheel`. Only the resource that runs the driver has the list, so each property is in one object, and no typed reading depends on one. The list holds no BLOB data. |
| <span id="status--activedevices"></span>`activeDevices` | [object](#statusactivedevices) | no | The INDI devices whose state the mount reads. The status holds what the driver reports in `ACTIVE_DEVICES`. The operator decides it from the resources around the device, so it is not in spec. |
| <span id="status--domepolicy"></span>`domePolicy` | [object](#statusdomepolicy) | no | Whether the dome's park locks the mount's park: `DOME_LOCKS` or `DOME_IGNORED`. The operator sets `DOME_LOCKS` when the observatory has a dome. The status holds what the driver reports in `DOME_POLICY`. The operator decides it from the resources around the device, so it is not in spec. |
| <span id="status--oncoordset"></span>`onCoordSet` | [object](#statusoncoordset) | no | What the mount does with the next coordinates that a client writes: slew, track, sync, or flip. The status holds what the driver reports in `ON_COORD_SET`. Clients such as KStars and Ekos change it. |
| <span id="status--equatorial"></span>`equatorial` | [object](#statusequatorial) | no | Where the mount points, in the equinox of the date (JNow). A client that writes it moves the mount, as `onCoordSet` says. The status holds what the driver reports in `EQUATORIAL_EOD_COORD`. Clients such as KStars and Ekos change it. |
| <span id="status--abortmotion"></span>`abortMotion` | [object](#statusabortmotion) | no | Stops every motion of the mount. The status holds what the driver reports in `TELESCOPE_ABORT_MOTION`. Clients such as KStars and Ekos change it. |
| <span id="status--trackmode"></span>`trackMode` | [object](#statustrackmode) | no | The rate that the mount tracks at, such as sidereal, solar, or lunar. The status holds what the driver reports in `TELESCOPE_TRACK_MODE`. `spec.trackMode` sets it. |
| <span id="status--tracking"></span>`tracking` | [object](#statustracking) | no | Turns tracking on or off. The status holds what the driver reports in `TELESCOPE_TRACK_STATE`. Clients such as KStars and Ekos change it. |
| <span id="status--trackrate"></span>`trackRate` | [object](#statustrackrate) | no | The rates of the custom track mode, in arcseconds per second. The status holds what the driver reports in `TELESCOPE_TRACK_RATE`. Clients such as KStars and Ekos change it. |
| <span id="status--home"></span>`home` | [object](#statushome) | no | Finds the home position, sets the current position as home, or moves the mount home. The status holds what the driver reports in `TELESCOPE_HOME`. Clients such as KStars and Ekos change it. |
| <span id="status--motionnorthsouth"></span>`motionNorthSouth` | [object](#statusmotionnorthsouth) | no | Moves the mount to the north or to the south while a client holds it on. The status holds what the driver reports in `TELESCOPE_MOTION_NS`. Clients such as KStars and Ekos change it. |
| <span id="status--motionwesteast"></span>`motionWestEast` | [object](#statusmotionwesteast) | no | Moves the mount to the west or to the east while a client holds it on. The status holds what the driver reports in `TELESCOPE_MOTION_WE`. Clients such as KStars and Ekos change it. |
| <span id="status--reversemotion"></span>`reverseMotion` | [object](#statusreversemotion) | no | The axes whose manual motion the driver reverses. The status holds what the driver reports in `TELESCOPE_REVERSE_MOTION`. `spec.reverseMotion` sets it. |
| <span id="status--slewrate"></span>`slewRate` | [object](#statusslewrate) | no | The rate of a manual motion, from the slowest to the fastest that the driver offers. The status holds what the driver reports in `TELESCOPE_SLEW_RATE`. Clients such as KStars and Ekos change it. |
| <span id="status--target"></span>`target` | [object](#statustarget) | no | Where the last slew goes, in the equinox of the date (JNow). The status holds what the driver reports in `TARGET_EOD_COORD`. |
| <span id="status--mounttype"></span>`mountType` | [object](#statusmounttype) | no | The type of the mount: alt-az, fork, or German equatorial. The status holds what the driver reports in `TELESCOPE_MOUNT_TYPE`. |
| <span id="status--time"></span>`time` | [object](#statustime) | no | The time that the mount uses to compute where the sky is. The status holds what the driver reports in `TIME_UTC`. The operator decides it from the resources around the device, so it is not in spec. |
| <span id="status--location"></span>`location` | [object](#statuslocation) | no | The site's location. The operator writes it from the `Observatory`'s `spec.location`. The status holds what the driver reports in `GEOGRAPHIC_COORD`. The operator decides it from the resources around the device, so it is not in spec. |
| <span id="status--park"></span>`park` | [object](#statuspark) | no | Parks or unparks the mount. The `state` action changes it. The status holds what the driver reports in `TELESCOPE_PARK`. Clients such as KStars and Ekos change it. |
| <span id="status--parkoption"></span>`parkOption` | [object](#statusparkoption) | no | Sets the park position to the current position or to the driver's default, or writes or deletes the driver's park data. The status holds what the driver reports in `TELESCOPE_PARK_OPTION`. Clients such as KStars and Ekos change it. |
| <span id="status--pierside"></span>`pierSide` | [object](#statuspierside) | no | The side of the pier that the mount is on. The status holds what the driver reports in `TELESCOPE_PIER_SIDE`. |
| <span id="status--simulatepierside"></span>`simulatePierSide` | [object](#statussimulatepierside) | no | Whether the driver computes the pier side, for a mount that does not report it. The status holds what the driver reports in `SIMULATE_PIER_SIDE`. `spec.simulatePierSide` sets it. |
| <span id="status--satellitetle"></span>`satelliteTle` | [object](#statussatellitetle) | no | The two-line element set of a satellite to track. The status holds what the driver reports in `SAT_TLE_TEXT`. Clients such as KStars and Ekos change it. |
| <span id="status--satellitepasswindow"></span>`satellitePassWindow` | [object](#statussatellitepasswindow) | no | The times when the satellite of `satelliteTle` is in view. The status holds what the driver reports in `SAT_PASS_WINDOW`. Clients such as KStars and Ekos change it. |
| <span id="status--satellitetracking"></span>`satelliteTracking` | [object](#statussatellitetracking) | no | Starts or halts the tracking of the satellite of `satelliteTle`. The status holds what the driver reports in `SAT_TRACKING_STAT`. Clients such as KStars and Ekos change it. |
| <span id="status--pec"></span>`pec` | [object](#statuspec) | no | Whether the mount plays back its periodic error correction. The status holds what the driver reports in `PEC`. `spec.pec` sets it. |
| <span id="status--joystick"></span>`joystick` | [object](#statusjoystick) | no | Whether the driver moves the mount from a joystick. The status holds what the driver reports in `USEJOYSTICK`. `spec.joystick` sets it. |
| <span id="status--joystickdevice"></span>`joystickDevice` | [object](#statusjoystickdevice) | no | The INDI device of the joystick. The status holds what the driver reports in `SNOOP_JOYSTICK`. The operator decides it from the resources around the device, so it is not in spec. |
| <span id="status--parkposition"></span>`parkPosition` | [object](#statusparkposition) | no | Where the mount parks, in the unit that each element's label names. Its elements depend on the mount's park type, such as right ascension and declination, or azimuth and altitude. The status holds what the driver reports in `TELESCOPE_PARK_POSITION`. `spec.parkPosition` sets it. |
| <span id="status--display"></span>`display` | [object](#statusdisplay) | no | The values of the typed fields as a person reads them, with their units, for the printer columns. |
| <span id="status--readings"></span>`readings` | [object](#statusreadings) | no | What the operator reads from several of the mount's properties, which no typed field holds alone. A field is absent until the driver sends what it comes from. |

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

The INDI server that runs the device. A client such as KStars reaches the device at this address by the name in `indiDevice`. The field is present only while the pod of the device's driver runs and is not stopping, which for a device that another resource serves is that resource's pod, and it names the server in that pod's `observatory.liken.sh/server` label. The driver stops on that server before the pod is deleted. A device that moves to another telescope names the server it left until that server deletes its pod. When a reservation ends, or the device moves to the shelf, the operator removes the field.

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

The INDI devices whose state the mount reads. The status holds what the driver reports in `ACTIVE_DEVICES`. The operator decides it from the resources around the device, so it is not in spec.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusactivedevices--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusactivedevices--gps"></span>`gps` | string | no | The GPS, for the site's location and time. The member is `ACTIVE_GPS`. |
| <span id="statusactivedevices--dome"></span>`dome` | string | no | The dome, for its park lock. The member is `ACTIVE_DOME`. |

### status.domePolicy

Whether the dome's park locks the mount's park: `DOME_LOCKS` or `DOME_IGNORED`. The operator sets `DOME_LOCKS` when the observatory has a dome. The status holds what the driver reports in `DOME_POLICY`. The operator decides it from the resources around the device, so it is not in spec.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdomepolicy--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusdomepolicy--value"></span>`value` | string | no | The name of the member that is on, such as `DOME_IGNORED`. Absent when no member is on. |

### status.onCoordSet

What the mount does with the next coordinates that a client writes: slew, track, sync, or flip. The status holds what the driver reports in `ON_COORD_SET`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusoncoordset--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusoncoordset--value"></span>`value` | string | no | The name of the member that is on, such as `TRACK`. Absent when no member is on. |

### status.equatorial

Where the mount points, in the equinox of the date (JNow). A client that writes it moves the mount, as `onCoordSet` says. The status holds what the driver reports in `EQUATORIAL_EOD_COORD`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusequatorial--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusequatorial--rightascension"></span>`rightAscension` | number | no | The right ascension, in hours from 0 to 24. The member is `RA`. |
| <span id="statusequatorial--declination"></span>`declination` | number | no | The declination, in degrees from -90 to 90. The member is `DEC`. |

### status.abortMotion

Stops every motion of the mount. The status holds what the driver reports in `TELESCOPE_ABORT_MOTION`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusabortmotion--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusabortmotion--value"></span>`value` | string | no | The name of the member that is on, such as `ABORT`. Absent when no member is on. |

### status.trackMode

The rate that the mount tracks at, such as sidereal, solar, or lunar. The status holds what the driver reports in `TELESCOPE_TRACK_MODE`. `spec.trackMode` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statustrackmode--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statustrackmode--elements"></span>`elements` | [\[\]object](#statustrackmodeelements) | no | Each element that the driver defines, in the driver's order. |

#### status.trackMode.elements[]

One element that the driver defines.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statustrackmodeelements--name"></span>`name` | string | yes | The element's name, as the driver defines it. |
| <span id="statustrackmodeelements--label"></span>`label` | string | no | The label that the driver gives the element. |
| <span id="statustrackmodeelements--active"></span>`active` | boolean | no | Whether the element is on. |

### status.tracking

Turns tracking on or off. The status holds what the driver reports in `TELESCOPE_TRACK_STATE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statustracking--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statustracking--value"></span>`value` | string | no | The name of the member that is on, such as `TRACK_ON`. Absent when no member is on. |

### status.trackRate

The rates of the custom track mode, in arcseconds per second. The status holds what the driver reports in `TELESCOPE_TRACK_RATE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statustrackrate--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statustrackrate--rightascension"></span>`rightAscension` | number | no | The rate of the right ascension axis, in arcseconds per second. The member is `TRACK_RATE_RA`. |
| <span id="statustrackrate--declination"></span>`declination` | number | no | The rate of the declination axis, in arcseconds per second. The member is `TRACK_RATE_DE`. |

### status.home

Finds the home position, sets the current position as home, or moves the mount home. The status holds what the driver reports in `TELESCOPE_HOME`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statushome--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statushome--value"></span>`value` | string | no | The name of the member that is on, such as `FIND`. Absent when no member is on. |

### status.motionNorthSouth

Moves the mount to the north or to the south while a client holds it on. The status holds what the driver reports in `TELESCOPE_MOTION_NS`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmotionnorthsouth--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusmotionnorthsouth--value"></span>`value` | string | no | The name of the member that is on, such as `MOTION_NORTH`. Absent when no member is on. |

### status.motionWestEast

Moves the mount to the west or to the east while a client holds it on. The status holds what the driver reports in `TELESCOPE_MOTION_WE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmotionwesteast--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusmotionwesteast--value"></span>`value` | string | no | The name of the member that is on, such as `MOTION_WEST`. Absent when no member is on. |

### status.reverseMotion

The axes whose manual motion the driver reverses. The status holds what the driver reports in `TELESCOPE_REVERSE_MOTION`. `spec.reverseMotion` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusreversemotion--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusreversemotion--values"></span>`values` | []string | no | The names of the members that are on, such as `REVERSE_NS`. |

### status.slewRate

The rate of a manual motion, from the slowest to the fastest that the driver offers. The status holds what the driver reports in `TELESCOPE_SLEW_RATE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusslewrate--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusslewrate--elements"></span>`elements` | [\[\]object](#statusslewrateelements) | no | Each element that the driver defines, in the driver's order. |

#### status.slewRate.elements[]

One element that the driver defines.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusslewrateelements--name"></span>`name` | string | yes | The element's name, as the driver defines it. |
| <span id="statusslewrateelements--label"></span>`label` | string | no | The label that the driver gives the element. |
| <span id="statusslewrateelements--active"></span>`active` | boolean | no | Whether the element is on. |

### status.target

Where the last slew goes, in the equinox of the date (JNow). The status holds what the driver reports in `TARGET_EOD_COORD`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statustarget--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statustarget--rightascension"></span>`rightAscension` | number | no | The right ascension, in hours from 0 to 24. The member is `RA`. |
| <span id="statustarget--declination"></span>`declination` | number | no | The declination, in degrees from -90 to 90. The member is `DEC`. |

### status.mountType

The type of the mount: alt-az, fork, or German equatorial. The status holds what the driver reports in `TELESCOPE_MOUNT_TYPE`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmounttype--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusmounttype--value"></span>`value` | string | no | The name of the member that is on, such as `ALTAZ`. Absent when no member is on. |

### status.time

The time that the mount uses to compute where the sky is. The status holds what the driver reports in `TIME_UTC`. The operator decides it from the resources around the device, so it is not in spec.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statustime--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statustime--utc"></span>`utc` | string | no | The time in UTC, in ISO 8601. The member is `UTC`. |
| <span id="statustime--offset"></span>`offset` | string | no | The offset of the local time from UTC, in hours. The member is `OFFSET`. |

### status.location

The site's location. The operator writes it from the `Observatory`'s `spec.location`. The status holds what the driver reports in `GEOGRAPHIC_COORD`. The operator decides it from the resources around the device, so it is not in spec.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuslocation--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statuslocation--latitude"></span>`latitude` | number | no | The latitude, in degrees north. The member is `LAT`. |
| <span id="statuslocation--longitude"></span>`longitude` | number | no | The longitude, in degrees east, from 0 to 360. The member is `LONG`. |
| <span id="statuslocation--elevation"></span>`elevation` | number | no | The elevation, in meters above sea level. The member is `ELEV`. |

### status.park

Parks or unparks the mount. The `state` action changes it. The status holds what the driver reports in `TELESCOPE_PARK`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuspark--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statuspark--value"></span>`value` | string | no | The name of the member that is on, such as `PARK`. Absent when no member is on. |

### status.parkOption

Sets the park position to the current position or to the driver's default, or writes or deletes the driver's park data. The status holds what the driver reports in `TELESCOPE_PARK_OPTION`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusparkoption--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusparkoption--value"></span>`value` | string | no | The name of the member that is on, such as `PARK_CURRENT`. Absent when no member is on. |

### status.pierSide

The side of the pier that the mount is on. The status holds what the driver reports in `TELESCOPE_PIER_SIDE`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuspierside--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statuspierside--value"></span>`value` | string | no | The name of the member that is on, such as `PIER_WEST`. Absent when no member is on. |

### status.simulatePierSide

Whether the driver computes the pier side, for a mount that does not report it. The status holds what the driver reports in `SIMULATE_PIER_SIDE`. `spec.simulatePierSide` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussimulatepierside--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statussimulatepierside--value"></span>`value` | string | no | The name of the member that is on, such as `SIMULATE_YES`. Absent when no member is on. |

### status.satelliteTle

The two-line element set of a satellite to track. The status holds what the driver reports in `SAT_TLE_TEXT`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussatellitetle--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statussatellitetle--value"></span>`value` | string | no | The value that the driver reports. |

### status.satellitePassWindow

The times when the satellite of `satelliteTle` is in view. The status holds what the driver reports in `SAT_PASS_WINDOW`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussatellitepasswindow--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statussatellitepasswindow--start"></span>`start` | string | no | The start of the pass, in UTC. The member is `SAT_PASS_WINDOW_START`. |
| <span id="statussatellitepasswindow--end"></span>`end` | string | no | The end of the pass, in UTC. The member is `SAT_PASS_WINDOW_END`. |

### status.satelliteTracking

Starts or halts the tracking of the satellite of `satelliteTle`. The status holds what the driver reports in `SAT_TRACKING_STAT`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussatellitetracking--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statussatellitetracking--value"></span>`value` | string | no | The name of the member that is on, such as `SAT_TRACK`. Absent when no member is on. |

### status.pec

Whether the mount plays back its periodic error correction. The status holds what the driver reports in `PEC`. `spec.pec` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuspec--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statuspec--value"></span>`value` | string | no | The name of the member that is on, such as `PEC OFF`. Absent when no member is on. |

### status.joystick

Whether the driver moves the mount from a joystick. The status holds what the driver reports in `USEJOYSTICK`. `spec.joystick` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusjoystick--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusjoystick--value"></span>`value` | string | no | The name of the member that is on, such as `ENABLE`. Absent when no member is on. |

### status.joystickDevice

The INDI device of the joystick. The status holds what the driver reports in `SNOOP_JOYSTICK`. The operator decides it from the resources around the device, so it is not in spec.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusjoystickdevice--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusjoystickdevice--value"></span>`value` | string | no | The value that the driver reports. |

### status.parkPosition

Where the mount parks, in the unit that each element's label names. Its elements depend on the mount's park type, such as right ascension and declination, or azimuth and altitude. The status holds what the driver reports in `TELESCOPE_PARK_POSITION`. `spec.parkPosition` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusparkposition--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusparkposition--elements"></span>`elements` | [\[\]object](#statusparkpositionelements) | no | Each element that the driver defines, in the driver's order. |

#### status.parkPosition.elements[]

One element that the driver defines.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusparkpositionelements--name"></span>`name` | string | yes | The element's name, as the driver defines it. |
| <span id="statusparkpositionelements--label"></span>`label` | string | no | The label that the driver gives the element. |
| <span id="statusparkpositionelements--value"></span>`value` | number | no | The element's value. The unit is the one that the element's label names. |

### status.display

The values of the typed fields as a person reads them, with their units, for the printer columns.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdisplay--rightascension"></span>`rightAscension` | string | no | The right ascension in hours, minutes, and seconds, such as `19h17m21s`. |
| <span id="statusdisplay--declination"></span>`declination` | string | no | The declination in degrees, arcminutes, and arcseconds, such as `+12°34′56″`. |

### status.readings

What the operator reads from several of the mount's properties, which no typed field holds alone. A field is absent until the driver sends what it comes from.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusreadings--state"></span>`state` | string | no | What the mount does: `Parking` or `Unparking` while `TELESCOPE_PARK` is `Busy`, `Parked` while its `PARK` member is on and its state is not `Alert`, `Slewing` while `EQUATORIAL_EOD_COORD` is `Busy`, `Tracking` while the `TRACK_ON` member of `TELESCOPE_TRACK_STATE` is on, and `Stopped` otherwise. The first that applies wins. Absent until the driver defines all three properties. One of: `Parked`, `Parking`, `Unparking`, `Stopped`, `Slewing`, `Tracking`. |
