---
title: Receiver
weight: 195
toc: true
---

<!-- Generated from deploy/receivers-crd.yaml by crdref. Do not edit. -->

# `Receiver`

A `Receiver` is a radio receiver, such as an RTL-SDR: an INDI driver with `SPECTROGRAPH_INTERFACE`.

## spec

One radio receiver, with INDI's `SPECTROGRAPH_INTERFACE`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--telescope"></span>`telescope` | string | no | The name of the `Telescope` that the device belongs to. Set this or `observatory`, not both. Optional. With neither `telescope` nor `observatory`, the device is on the shelf, and the operator creates nothing for it. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spec--observatory"></span>`observatory` | string | no | The name of the `Observatory` that the device belongs to, when no single telescope owns it. The device then runs on the observatory's own INDI server. Set this or `telescope`, not both. Optional. With neither `telescope` nor `observatory`, the device is on the shelf, and the operator creates nothing for it. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spec--driver"></span>`driver` | [object](#specdriver) | no | The INDI driver that runs the device. With no `image`, the operator runs the image that the `indi` build lists for the driver, so `name` must then be an INDI driver that the image holds, such as `indi_simulator_ccd`. Set `driver` or `device`, not both. |
| <span id="spec--device"></span>`device` | [object](#specdevice) | no | The resource whose driver serves this device, in place of a driver of its own. INDI puts every interface of one driver on one device, so a camera with a built-in filter wheel serves a `FilterWheel`, and a flat panel with a motorized cover serves a `DustCap`. The resource it names runs the driver, holds the pod, the claim, and the power, and must be on the same INDI server. The operator connects that device before this one's activation, and runs this one's deactivation before that device stops. Set `driver` or `device`, not both. The field cannot change, and a resource that runs a driver cannot gain it: delete the resource and create it again to move it to another device. |
| <span id="spec--power"></span>`power` | [object](#specpower) | no | The `Switch` output that powers the device. Activation switches the output on before the device's pod starts, and deactivation switches it off after the pod stops. Optional. With no power, a person powers the device on and off by hand. |
| <span id="spec--claim"></span>`claim` | object | no | A `ResourceClaimSpec` of the `resource.k8s.io` API, for real hardware. The operator creates a `ResourceClaim` from it for the device's pod. The API server validates it when the operator creates the claim. Optional. A simulator needs no claim. |
| <span id="spec--receiversettings"></span>`receiverSettings` | [object](#specreceiversettings) | no | The tuning of the receiver. The operator writes it to `RECEIVER_SETTINGS` once, in the `Configure` step of each reservation. Optional. |
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

### spec.receiverSettings

The tuning of the receiver. The operator writes it to `RECEIVER_SETTINGS` once, in the `Configure` step of each reservation. Optional.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specreceiversettings--gain"></span>`gain` | number | no | The gain, in the driver's own units. The member is `RECEIVER_GAIN`. |
| <span id="specreceiversettings--frequency"></span>`frequency` | number | no | The center frequency, in hertz. The member is `RECEIVER_FREQUENCY`. |
| <span id="specreceiversettings--bandwidth"></span>`bandwidth` | number | no | The bandwidth, in hertz. The member is `RECEIVER_BANDWIDTH`. |
| <span id="specreceiversettings--bitspersample"></span>`bitsPerSample` | number | no | The depth of each sample, in bits. The member is `RECEIVER_BITSPERSAMPLE`. |
| <span id="specreceiversettings--samplerate"></span>`sampleRate` | number | no | The sample rate, in hertz. The member is `RECEIVER_SAMPLERATE`. |
| <span id="specreceiversettings--antenna"></span>`antenna` | number | no | The number of the antenna input. The member is `RECEIVER_ANTENNA`. |

### spec.activation[]

One action, which names `job`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
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
| <span id="specactivationrequires--status"></span>`status` | string | no | The status that the condition must have: `True`, `False`, or `Unknown`. The default is `True`. One of: `True`, `False`, `Unknown`. Default: `True`. |

#### spec.activation[].after[]

One resource, by its kind and name.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specactivationafter--kind"></span>`kind` | string | yes | The kind of the resource. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `Switch`, `Receiver`, `Guider`. |
| <span id="specactivationafter--name"></span>`name` | string | no | The name of the resource. With no `name`, the reference names every resource of the kind in the observatory, or the resource's own `Observatory` or `Telescope`. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |

### spec.deactivation[]

One action, which names `job`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
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

One action, which names `job`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
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
| <span id="status--limits"></span>`limits` | map[string]map[string]object | no | The range that the driver gives each number of this resource's typed fields that a client can write, by INDI property and then by member, such as `limits.CCD_TEMPERATURE.CCD_TEMPERATURE_VALUE`. Each range has `min` and `max`, the least and the greatest value that the driver accepts, and `step` when the driver states one. The schema describes each typed field, and the driver chooses only these ranges, such as a camera's least and greatest temperature or its range of gain. |
| <span id="status--properties"></span>`properties` | [\[\]object](#statusproperties) | no | Each property of the INDI device that no typed field holds, as the driver defines it, with the values of its last update: a vendor's own properties, the properties that every INDI device has, such as `DEBUG`, and the standard properties of an interface that no resource on the device is, such as the `FILTER_SLOT` of a camera with no `FilterWheel`. Only the resource that runs the driver has the list, so each property is in one object, and no typed reading depends on one. The list holds no BLOB data. |
| <span id="status--activedevices"></span>`activeDevices` | [object](#statusactivedevices) | no | The INDI devices whose state the receiver writes into the FITS header of each integration. The status holds what the driver reports in `ACTIVE_DEVICES`. The operator decides it from the resources around the device, so it is not in spec. |
| <span id="status--receiversettings"></span>`receiverSettings` | [object](#statusreceiversettings) | no | The tuning of the receiver. The status holds what the driver reports in `RECEIVER_SETTINGS`. `spec.receiverSettings` sets it. |
| <span id="status--temperature"></span>`temperature` | [object](#statustemperature) | no | The temperature of the receiver, in degrees Celsius. A client that writes it sets the cooler's setpoint. The status holds what the driver reports in `SENSOR_TEMPERATURE`. Clients such as KStars and Ekos change it. |
| <span id="status--integration"></span>`integration` | [object](#statusintegration) | no | The length of an integration, in seconds. While the integration runs, the driver counts it down. The status holds what the driver reports in `SENSOR_INTEGRATION`. Clients such as KStars and Ekos change it. |
| <span id="status--fitsheader"></span>`fitsHeader` | [object](#statusfitsheader) | no | The observer and the object that the FITS header of each integration names. The status holds what the driver reports in `FITS_HEADER`. Clients such as KStars and Ekos change it. |
| <span id="status--data"></span>`data` | [object](#statusdata) | no | The data of the last integration. The operator never asks for its data, so the status holds its state alone. The status holds what the driver reports in `SENSOR`. |
| <span id="status--uploadmode"></span>`uploadMode` | [object](#statusuploadmode) | no | Where the driver sends each integration: to the client, to a local file, or to both. The status holds what the driver reports in `UPLOAD_MODE`. Clients such as KStars and Ekos change it. |
| <span id="status--uploadsettings"></span>`uploadSettings` | [object](#statusuploadsettings) | no | Where the driver saves an integration that it saves to a local file. The status holds what the driver reports in `UPLOAD_SETTINGS`. Clients such as KStars and Ekos change it. |
| <span id="status--datastream"></span>`dataStream` | [object](#statusdatastream) | no | Turns the data stream of the receiver on or off. The status holds what the driver reports in `SENSOR_DATA_STREAM`. Clients such as KStars and Ekos change it. |
| <span id="status--streamdepth"></span>`streamDepth` | [object](#statusstreamdepth) | no | The depth of each sample of the data stream, 8 or 16 bits. The status holds what the driver reports in `STREAM_FULL_DEPTH`. Clients such as KStars and Ekos change it. |
| <span id="status--streamdelay"></span>`streamDelay` | [object](#statusstreamdelay) | no | The delay between the frames of the data stream, in seconds. The status holds what the driver reports in `STREAM_DELAY`. |
| <span id="status--streamingexposure"></span>`streamingExposure` | [object](#statusstreamingexposure) | no | The integration of each frame of the data stream. The status holds what the driver reports in `STREAMING_EXPOSURE`. Clients such as KStars and Ekos change it. |
| <span id="status--fps"></span>`fps` | [object](#statusfps) | no | The frame rate of the data stream, in frames per second. The status holds what the driver reports in `FPS`. |
| <span id="status--recordstream"></span>`recordStream` | [object](#statusrecordstream) | no | Starts or stops a recording of the data stream, with no end, for the time, or for the frames in `recordOptions`. The status holds what the driver reports in `RECORD_STREAM`. Clients such as KStars and Ekos change it. |
| <span id="status--recordfile"></span>`recordFile` | [object](#statusrecordfile) | no | The file that a recording of the data stream writes. The status holds what the driver reports in `RECORD_FILE`. Clients such as KStars and Ekos change it. |
| <span id="status--recordoptions"></span>`recordOptions` | [object](#statusrecordoptions) | no | When a recording of the data stream ends. The status holds what the driver reports in `RECORD_OPTIONS`. Clients such as KStars and Ekos change it. |
| <span id="status--streamencoder"></span>`streamEncoder` | [object](#statusstreamencoder) | no | The encoder of the data stream, raw or MJPEG. The status holds what the driver reports in `SENSOR_STREAM_ENCODER`. Clients such as KStars and Ekos change it. |
| <span id="status--streamrecorder"></span>`streamRecorder` | [object](#statusstreamrecorder) | no | The format of a recording of the data stream. The status holds what the driver reports in `SENSOR_STREAM_RECORDER`. Clients such as KStars and Ekos change it. |
| <span id="status--streamlimits"></span>`streamLimits` | [object](#statusstreamlimits) | no | The limits of the data stream. The status holds what the driver reports in `LIMITS`. Clients such as KStars and Ekos change it. |
| <span id="status--dspconvolution"></span>`dspConvolution` | [object](#statusdspconvolution) | no | Turns the convolution of each integration on or off. The status holds what the driver reports in `DSP_ACTIVATE_CONVOLUTION`. Clients such as KStars and Ekos change it. |
| <span id="status--dspdft"></span>`dspDft` | [object](#statusdspdft) | no | Turns the discrete Fourier transform of each integration on or off. The status holds what the driver reports in `DSP_ACTIVATE_DFT`. Clients such as KStars and Ekos change it. |
| <span id="status--dspidft"></span>`dspIdft` | [object](#statusdspidft) | no | Turns the inverse discrete Fourier transform of each integration on or off. The status holds what the driver reports in `DSP_ACTIVATE_IDFT`. Clients such as KStars and Ekos change it. |
| <span id="status--dspspectrum"></span>`dspSpectrum` | [object](#statusdspspectrum) | no | Turns the spectrum of each integration on or off. The status holds what the driver reports in `DSP_ACTIVATE_SPECTRUM`. Clients such as KStars and Ekos change it. |
| <span id="status--dsphistogram"></span>`dspHistogram` | [object](#statusdsphistogram) | no | Turns the histogram of each integration on or off. The status holds what the driver reports in `DSP_ACTIVATE_HISTOGRAM`. Clients such as KStars and Ekos change it. |
| <span id="status--dspwavelets"></span>`dspWavelets` | [object](#statusdspwavelets) | no | Turns the wavelet transform of each integration on or off. The status holds what the driver reports in `DSP_ACTIVATE_WAVELETS`. Clients such as KStars and Ekos change it. |
| <span id="status--display"></span>`display` | [object](#statusdisplay) | no | The values of the typed fields as a person reads them, with their units, for the printer columns. |

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

The INDI devices whose state the receiver writes into the FITS header of each integration. The status holds what the driver reports in `ACTIVE_DEVICES`. The operator decides it from the resources around the device, so it is not in spec.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusactivedevices--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusactivedevices--telescope"></span>`telescope` | string | no | The mount, for where the antenna points. The member is `ACTIVE_TELESCOPE`. |
| <span id="statusactivedevices--gps"></span>`gps` | string | no | The GPS, for the site's location and time. The member is `ACTIVE_GPS`. |

### status.receiverSettings

The tuning of the receiver. The status holds what the driver reports in `RECEIVER_SETTINGS`. `spec.receiverSettings` sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusreceiversettings--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusreceiversettings--gain"></span>`gain` | number | no | The gain, in the driver's own units. The member is `RECEIVER_GAIN`. |
| <span id="statusreceiversettings--frequency"></span>`frequency` | number | no | The center frequency, in hertz. The member is `RECEIVER_FREQUENCY`. |
| <span id="statusreceiversettings--bandwidth"></span>`bandwidth` | number | no | The bandwidth, in hertz. The member is `RECEIVER_BANDWIDTH`. |
| <span id="statusreceiversettings--bitspersample"></span>`bitsPerSample` | number | no | The depth of each sample, in bits. The member is `RECEIVER_BITSPERSAMPLE`. |
| <span id="statusreceiversettings--samplerate"></span>`sampleRate` | number | no | The sample rate, in hertz. The member is `RECEIVER_SAMPLERATE`. |
| <span id="statusreceiversettings--antenna"></span>`antenna` | number | no | The number of the antenna input. The member is `RECEIVER_ANTENNA`. |

### status.temperature

The temperature of the receiver, in degrees Celsius. A client that writes it sets the cooler's setpoint. The status holds what the driver reports in `SENSOR_TEMPERATURE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statustemperature--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statustemperature--value"></span>`value` | number | no | The value that the driver reports. The unit is degrees Celsius. |

### status.integration

The length of an integration, in seconds. While the integration runs, the driver counts it down. The status holds what the driver reports in `SENSOR_INTEGRATION`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusintegration--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusintegration--value"></span>`value` | number | no | The value that the driver reports. The unit is seconds. |

### status.fitsHeader

The observer and the object that the FITS header of each integration names. The status holds what the driver reports in `FITS_HEADER`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusfitsheader--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusfitsheader--observer"></span>`observer` | string | no | The name of the observer. The member is `FITS_OBSERVER`. |
| <span id="statusfitsheader--object"></span>`object` | string | no | The name of the object. The member is `FITS_OBJECT`. |

### status.data

The data of the last integration. The operator never asks for its data, so the status holds its state alone. The status holds what the driver reports in `SENSOR`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdata--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |

### status.uploadMode

Where the driver sends each integration: to the client, to a local file, or to both. The status holds what the driver reports in `UPLOAD_MODE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusuploadmode--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusuploadmode--value"></span>`value` | string | no | The name of the member that is on, such as `UPLOAD_CLIENT`. Absent when no member is on. |

### status.uploadSettings

Where the driver saves an integration that it saves to a local file. The status holds what the driver reports in `UPLOAD_SETTINGS`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusuploadsettings--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusuploadsettings--directory"></span>`directory` | string | no | The directory, in the driver's container. The member is `UPLOAD_DIR`. |
| <span id="statusuploadsettings--prefix"></span>`prefix` | string | no | The start of each file's name. The member is `UPLOAD_PREFIX`. |

### status.dataStream

Turns the data stream of the receiver on or off. The status holds what the driver reports in `SENSOR_DATA_STREAM`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdatastream--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusdatastream--value"></span>`value` | string | no | The name of the member that is on, such as `STREAM_ON`. Absent when no member is on. |

### status.streamDepth

The depth of each sample of the data stream, 8 or 16 bits. The status holds what the driver reports in `STREAM_FULL_DEPTH`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstreamdepth--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusstreamdepth--value"></span>`value` | string | no | The name of the member that is on, such as `FULL_DEPTH_8BIT`. Absent when no member is on. |

### status.streamDelay

The delay between the frames of the data stream, in seconds. The status holds what the driver reports in `STREAM_DELAY`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstreamdelay--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusstreamdelay--value"></span>`value` | number | no | The value that the driver reports. The unit is seconds. |

### status.streamingExposure

The integration of each frame of the data stream. The status holds what the driver reports in `STREAMING_EXPOSURE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstreamingexposure--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusstreamingexposure--duration"></span>`duration` | number | no | The length of each integration, in seconds. The member is `STREAMING_EXPOSURE_VALUE`. |
| <span id="statusstreamingexposure--divisor"></span>`divisor` | number | no | The stream sends one frame in this many. 1 sends every frame. The member is `STREAMING_DIVISOR_VALUE`. |

### status.fps

The frame rate of the data stream, in frames per second. The status holds what the driver reports in `FPS`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusfps--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusfps--instant"></span>`instant` | number | no | The rate of the last frames, in frames per second. The member is `EST_FPS`. |
| <span id="statusfps--average"></span>`average` | number | no | The average rate over one second, in frames per second. The member is `AVG_FPS`. |

### status.recordStream

Starts or stops a recording of the data stream, with no end, for the time, or for the frames in `recordOptions`. The status holds what the driver reports in `RECORD_STREAM`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusrecordstream--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusrecordstream--value"></span>`value` | string | no | The name of the member that is on, such as `RECORD_ON`. Absent when no member is on. |

### status.recordFile

The file that a recording of the data stream writes. The status holds what the driver reports in `RECORD_FILE`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusrecordfile--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusrecordfile--directory"></span>`directory` | string | no | The directory, in the driver's container. The member is `RECORD_FILE_DIR`. |
| <span id="statusrecordfile--name"></span>`name` | string | no | The file's name. The member is `RECORD_FILE_NAME`. |

### status.recordOptions

When a recording of the data stream ends. The status holds what the driver reports in `RECORD_OPTIONS`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusrecordoptions--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusrecordoptions--duration"></span>`duration` | number | no | The length of a recording, in seconds. The member is `RECORD_DURATION`. |
| <span id="statusrecordoptions--frames"></span>`frames` | number | no | The length of a recording, in frames. The member is `RECORD_FRAME_TOTAL`. |

### status.streamEncoder

The encoder of the data stream, raw or MJPEG. The status holds what the driver reports in `SENSOR_STREAM_ENCODER`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstreamencoder--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusstreamencoder--value"></span>`value` | string | no | The name of the member that is on, such as `RAW`. Absent when no member is on. |

### status.streamRecorder

The format of a recording of the data stream. The status holds what the driver reports in `SENSOR_STREAM_RECORDER`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstreamrecorder--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusstreamrecorder--value"></span>`value` | string | no | The name of the member that is on, such as `SER`. Absent when no member is on. |

### status.streamLimits

The limits of the data stream. The status holds what the driver reports in `LIMITS`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusstreamlimits--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusstreamlimits--buffermax"></span>`bufferMax` | number | no | The largest buffer of frames, in megabytes. The member is `LIMITS_BUFFER_MAX`. |
| <span id="statusstreamlimits--previewfps"></span>`previewFps` | number | no | The greatest rate that the stream sends to a client, in frames per second. The member is `LIMITS_PREVIEW_FPS`. |

### status.dspConvolution

Turns the convolution of each integration on or off. The status holds what the driver reports in `DSP_ACTIVATE_CONVOLUTION`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdspconvolution--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusdspconvolution--value"></span>`value` | string | no | The name of the member that is on, such as `DSP_ACTIVATE_ON`. Absent when no member is on. |

### status.dspDft

Turns the discrete Fourier transform of each integration on or off. The status holds what the driver reports in `DSP_ACTIVATE_DFT`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdspdft--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusdspdft--value"></span>`value` | string | no | The name of the member that is on, such as `DSP_ACTIVATE_ON`. Absent when no member is on. |

### status.dspIdft

Turns the inverse discrete Fourier transform of each integration on or off. The status holds what the driver reports in `DSP_ACTIVATE_IDFT`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdspidft--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusdspidft--value"></span>`value` | string | no | The name of the member that is on, such as `DSP_ACTIVATE_ON`. Absent when no member is on. |

### status.dspSpectrum

Turns the spectrum of each integration on or off. The status holds what the driver reports in `DSP_ACTIVATE_SPECTRUM`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdspspectrum--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusdspspectrum--value"></span>`value` | string | no | The name of the member that is on, such as `DSP_ACTIVATE_ON`. Absent when no member is on. |

### status.dspHistogram

Turns the histogram of each integration on or off. The status holds what the driver reports in `DSP_ACTIVATE_HISTOGRAM`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdsphistogram--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusdsphistogram--value"></span>`value` | string | no | The name of the member that is on, such as `DSP_ACTIVATE_ON`. Absent when no member is on. |

### status.dspWavelets

Turns the wavelet transform of each integration on or off. The status holds what the driver reports in `DSP_ACTIVATE_WAVELETS`. Clients such as KStars and Ekos change it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdspwavelets--state"></span>`state` | string | no | The property's state: `Busy` while the driver carries out a change, `Ok` when the last change ended well, `Alert` when it failed, and `Idle` before the first change. One of: `Idle`, `Ok`, `Busy`, `Alert`. |
| <span id="statusdspwavelets--value"></span>`value` | string | no | The name of the member that is on, such as `DSP_ACTIVATE_ON`. Absent when no member is on. |

### status.display

The values of the typed fields as a person reads them, with their units, for the printer columns.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdisplay--frequency"></span>`frequency` | string | no | The center frequency in megahertz, such as `1420.406 MHz`. |
