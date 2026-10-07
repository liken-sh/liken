---
title: Telescope
weight: 105
toc: true
---

<!-- Generated from deploy/telescopes-crd.yaml by crdref. Do not edit. -->

A `Telescope` is a mount with its optics and its instruments. It has one INDI server, with its `Mount` and the devices of its trains, while a `Reservation` of it is active. KStars connects one Ekos profile to that server.

## spec

Where the telescope is.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--observatory"></span>`observatory` | string | yes | The name of the `Observatory` that the telescope is in. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spec--activation"></span>`activation` | [\[\]object](#specactivation) | no | The actions that run, in order, as this resource's `Telescope` or `Observatory` turns `Active`, while every device is still connected. For a `Telescope` or an `Observatory`, that is the resource itself. The `Reservation`'s `Activation` step waits for them. Each action is a target state, so a run that an operator restart interrupted runs again each action that is not `Done`. Optional. |
| <span id="spec--deactivation"></span>`deactivation` | [\[\]object](#specdeactivation) | no | The actions that run, in order, as this resource's `Telescope` or `Observatory` stops being `Active`, while every device is still connected. For a `Telescope` or an `Observatory`, that is the resource itself. The `Reservation`'s `Deactivation` step waits for them. Each action is a target state, so a run that an operator restart interrupted runs again each action that is not `Done`. Optional. |
| <span id="spec--triggers"></span>`triggers` | [\[\]object](#spectriggers) | no | Triggers on conditions. Each one runs its actions once for each transition of its condition to the status it names, while the resource is active: from when its activation is done until its deactivation begins. A condition that holds when the activation ends runs then. A run that began runs to its end, unless the resource's deactivation begins, and a resource runs one procedure at a time. Optional. |

### spec.activation[]

One action, which names `job`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specactivation--job"></span>`job` | [object](#specactivationjob) | no | A container that the operator runs once as a Kubernetes `Job` in the operator's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes. |
| <span id="specactivation--timeout"></span>`timeout` | string | no | How long the action may take, its waits for `requires` and `after` included, as a duration such as `20m`. An action that passes it fails. Optional. Each action has a default. |
| <span id="specactivation--requires"></span>`requires` | [\[\]object](#specactivationrequires) | no | The conditions that must hold before the action runs. The operator waits for each one until the action's timeout. They bind only the actions that the operator runs: a move that a person makes in KStars is stopped only by the drivers' park locks. |
| <span id="specactivation--after"></span>`after` | [\[\]object](#specactivationafter) | no | The resources whose runs for the same event must end before the action runs: the same lifecycle step, or the same transition of the same condition. A resource with no such run is not waited for. A resource in a later tier of the tree is waited for until the action's timeout, and the action then fails. |

#### spec.activation[].job

A container that the operator runs once as a Kubernetes `Job` in the operator's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes.

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
| <span id="specactivationrequires--kind"></span>`kind` | string | no | The kind of the resource whose condition it reads. With no `kind`, the condition is the resource's own. A `kind` of `Observatory` or `Telescope` with no `name` means the resource's own observatory or telescope. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `SkyQualityMeter`, `Switch`, `Receiver`, `Guider`. |
| <span id="specactivationrequires--name"></span>`name` | string | no | The name of the resource, in this namespace. A device kind needs a name. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="specactivationrequires--type"></span>`type` | string | yes | The condition's type, such as `Safe` or `Parked`. |
| <span id="specactivationrequires--status"></span>`status` | string | no | The status that the condition must have: `True`, `False`, or `Unknown`. The default is `True`. One of: `True`, `False`, `Unknown`. Default: `True`. |

#### spec.activation[].after[]

One resource, by its kind and name.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specactivationafter--kind"></span>`kind` | string | yes | The kind of the resource. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `SkyQualityMeter`, `Switch`, `Receiver`, `Guider`. |
| <span id="specactivationafter--name"></span>`name` | string | no | The name of the resource. With no `name`, the reference names every resource of the kind in the observatory, or the resource's own `Observatory` or `Telescope`. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |

### spec.deactivation[]

One action, which names `job`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdeactivation--job"></span>`job` | [object](#specdeactivationjob) | no | A container that the operator runs once as a Kubernetes `Job` in the operator's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes. |
| <span id="specdeactivation--timeout"></span>`timeout` | string | no | How long the action may take, its waits for `requires` and `after` included, as a duration such as `20m`. An action that passes it fails. Optional. Each action has a default. |
| <span id="specdeactivation--requires"></span>`requires` | [\[\]object](#specdeactivationrequires) | no | The conditions that must hold before the action runs. The operator waits for each one until the action's timeout. They bind only the actions that the operator runs: a move that a person makes in KStars is stopped only by the drivers' park locks. |
| <span id="specdeactivation--after"></span>`after` | [\[\]object](#specdeactivationafter) | no | The resources whose runs for the same event must end before the action runs: the same lifecycle step, or the same transition of the same condition. A resource with no such run is not waited for. A resource in a later tier of the tree is waited for until the action's timeout, and the action then fails. |

#### spec.deactivation[].job

A container that the operator runs once as a Kubernetes `Job` in the operator's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes.

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
| <span id="specdeactivationrequires--kind"></span>`kind` | string | no | The kind of the resource whose condition it reads. With no `kind`, the condition is the resource's own. A `kind` of `Observatory` or `Telescope` with no `name` means the resource's own observatory or telescope. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `SkyQualityMeter`, `Switch`, `Receiver`, `Guider`. |
| <span id="specdeactivationrequires--name"></span>`name` | string | no | The name of the resource, in this namespace. A device kind needs a name. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="specdeactivationrequires--type"></span>`type` | string | yes | The condition's type, such as `Safe` or `Parked`. |
| <span id="specdeactivationrequires--status"></span>`status` | string | no | The status that the condition must have: `True`, `False`, or `Unknown`. The default is `True`. One of: `True`, `False`, `Unknown`. Default: `True`. |

#### spec.deactivation[].after[]

One resource, by its kind and name.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdeactivationafter--kind"></span>`kind` | string | yes | The kind of the resource. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `SkyQualityMeter`, `Switch`, `Receiver`, `Guider`. |
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
| <span id="spectriggerswhen--kind"></span>`kind` | string | no | The kind of the resource whose condition it reads. With no `kind`, the condition is the resource's own. A `kind` of `Observatory` or `Telescope` with no `name` means the resource's own observatory or telescope. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `SkyQualityMeter`, `Switch`, `Receiver`, `Guider`. |
| <span id="spectriggerswhen--name"></span>`name` | string | no | The name of the resource, in this namespace. A device kind needs a name. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spectriggerswhen--type"></span>`type` | string | yes | The condition's type, such as `Safe` or `Parked`. |
| <span id="spectriggerswhen--status"></span>`status` | string | no | The status that the condition must have: `True`, `False`, or `Unknown`. The default is `True`. One of: `True`, `False`, `Unknown`. Default: `True`. |
| <span id="spectriggerswhen--for"></span>`for` | string | no | How long the status must hold before the actions run, as a duration such as `20m`. A change of the status before then cancels the run. Optional. |

#### spec.triggers[].run[]

One action, which names `job`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectriggersrun--job"></span>`job` | [object](#spectriggersrunjob) | no | A container that the operator runs once as a Kubernetes `Job` in the operator's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes. |
| <span id="spectriggersrun--timeout"></span>`timeout` | string | no | How long the action may take, its waits for `requires` and `after` included, as a duration such as `20m`. An action that passes it fails. Optional. Each action has a default. |
| <span id="spectriggersrun--requires"></span>`requires` | [\[\]object](#spectriggersrunrequires) | no | The conditions that must hold before the action runs. The operator waits for each one until the action's timeout. They bind only the actions that the operator runs: a move that a person makes in KStars is stopped only by the drivers' park locks. |
| <span id="spectriggersrun--after"></span>`after` | [\[\]object](#spectriggersrunafter) | no | The resources whose runs for the same event must end before the action runs: the same lifecycle step, or the same transition of the same condition. A resource with no such run is not waited for. A resource in a later tier of the tree is waited for until the action's timeout, and the action then fails. |

#### spec.triggers[].run[].job

A container that the operator runs once as a Kubernetes `Job` in the operator's namespace, for what no other action does, such as switching a dew heater's relay or posting to a webhook. The resource owns the `Job`. The action is `Done` when the `Job` succeeds, and `Failed` with the `Job`'s reason and message when it fails. The `Job` has no retry, and its `activeDeadlineSeconds` is the action's timeout. Kubernetes deletes it an hour after it ends. The environment adds `LIKEN_OBSERVATORY`, `LIKEN_TELESCOPE` for a resource of a telescope, `LIKEN_RESOURCE` such as `Dome/lab`, `LIKEN_TRIGGER` such as `activation` or `triggers[0]`, and `INDI_HOST` and `INDI_PORT` of the resource's INDI server. The container runs as user 1000 with no capabilities, on a read-only root filesystem with a writable `/tmp`. The default timeout is 10 minutes.

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
| <span id="spectriggersrunrequires--kind"></span>`kind` | string | no | The kind of the resource whose condition it reads. With no `kind`, the condition is the resource's own. A `kind` of `Observatory` or `Telescope` with no `name` means the resource's own observatory or telescope. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `SkyQualityMeter`, `Switch`, `Receiver`, `Guider`. |
| <span id="spectriggersrunrequires--name"></span>`name` | string | no | The name of the resource, in this namespace. A device kind needs a name. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spectriggersrunrequires--type"></span>`type` | string | yes | The condition's type, such as `Safe` or `Parked`. |
| <span id="spectriggersrunrequires--status"></span>`status` | string | no | The status that the condition must have: `True`, `False`, or `Unknown`. The default is `True`. One of: `True`, `False`, `Unknown`. Default: `True`. |

#### spec.triggers[].run[].after[]

One resource, by its kind and name.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spectriggersrunafter--kind"></span>`kind` | string | yes | The kind of the resource. One of: `Observatory`, `Telescope`, `OpticalTube`, `OpticalTrain`, `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `SkyQualityMeter`, `Switch`, `Receiver`, `Guider`. |
| <span id="spectriggersrunafter--name"></span>`name` | string | no | The name of the resource. With no `name`, the reference names every resource of the kind in the observatory, or the resource's own `Observatory` or `Telescope`. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |

## status

What the operator observes. The operator writes it, and a person reads it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the operator last acted on. |
| <span id="status--phase"></span>`phase` | string | no | The state in one word. `Idle`: no reservation needs it, and nothing runs. `Activating`: a reservation's activation steps run. `Ready`: everything it needs runs and is connected. `Deactivating`: a reservation's deactivation steps run. `Error`: a step failed, and the `Ready` condition gives the reason. One of: `Idle`, `Activating`, `Ready`, `Deactivating`, `Error`. |
| <span id="status--procedures"></span>`procedures` | [\[\]object](#statusprocedures) | no | The last run of each trigger of the resource's procedures: `activation`, `deactivation`, and `triggers[0]` and so on for each item of `spec.triggers`. An operator that restarts resumes a run from this record. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Ready` is `True` while the telescope's server runs and every device of the telescope is connected. `ParentFound` is `False` when the `Observatory` does not exist. `Active` is `True` from the start of its reservation's `Activation` step until the start of its `Deactivation` step. Its `lastTransitionTime` is the transition that the activation and deactivation procedures answer. |
| <span id="status--server"></span>`server` | [object](#statusserver) | no | The telescope's INDI server, while it runs. KStars and `astrophotography-operator` connect to its `host` and `port`. |
| <span id="status--reservation"></span>`reservation` | [object](#statusreservation) | no | The active `Reservation` of this telescope. |
| <span id="status--tubes"></span>`tubes` | []string | no | Each `OpticalTube` of this telescope. |
| <span id="status--trains"></span>`trains` | [\[\]object](#statustrains) | no | Each `OpticalTrain` of this telescope, with its devices. |
| <span id="status--devices"></span>`devices` | [\[\]object](#statusdevices) | no | Each device whose parent is this telescope. |
| <span id="status--display"></span>`display` | [object](#statusdisplay) | no | What the printer columns show. |
| <span id="status--guider"></span>`guider` | [object](#statusguider) | no | The telescope's `Guider`, and whether it is ready. |

### status.procedures[]

The last run of one trigger.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusprocedures--trigger"></span>`trigger` | string | yes | The trigger: `activation`, `deactivation`, or `triggers[0]` for the first item of `spec.triggers`. |
| <span id="statusprocedures--since"></span>`since` | string | no | The transition time of the condition that the run answers: the `lastTransitionTime` of `Active` for activation and deactivation. |
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

### status.server

The telescope's INDI server, while it runs. KStars and `astrophotography-operator` connect to its `host` and `port`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusserver--service"></span>`service` | string | yes | The name of the server's `Service`. |
| <span id="statusserver--host"></span>`host` | string | yes | The `Service`'s DNS name in the cluster. |
| <span id="statusserver--port"></span>`port` | integer | yes | The INDI port of the `Service`, 7624. |
| <span id="statusserver--pod"></span>`pod` | string | no | The name of the server's pod. |
| <span id="statusserver--node"></span>`node` | string | no | The node that runs the server's pod. |

### status.reservation

The active `Reservation` of this telescope.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusreservation--name"></span>`name` | string | yes | The reservation's name. |
| <span id="statusreservation--holder"></span>`holder` | string | no | The reservation's `spec.holder`. |

### status.trains[]

One train.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statustrains--name"></span>`name` | string | yes | The train's name. |
| <span id="statustrains--opticaltube"></span>`opticalTube` | string | no | The train's `spec.opticalTube`. |
| <span id="statustrains--devices"></span>`devices` | [\[\]object](#statustrainsdevices) | no | Each device on the train. |

#### status.trains[].devices[]

One device, with its phase.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statustrainsdevices--kind"></span>`kind` | string | yes | The device's kind. One of: `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `SkyQualityMeter`, `Switch`, `Receiver`. |
| <span id="statustrainsdevices--name"></span>`name` | string | yes | The device's name. |
| <span id="statustrainsdevices--phase"></span>`phase` | string | no | The device's `status.phase`. One of: `Inventory`, `Idle`, `Starting`, `Connecting`, `Connected`, `Disconnecting`, `Error`. |

### status.devices[]

One device, with its phase.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdevices--kind"></span>`kind` | string | yes | The device's kind. One of: `Mount`, `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel`, `PolarAligner`, `GPS`, `Dome`, `WeatherStation`, `SkyQualityMeter`, `Switch`, `Receiver`. |
| <span id="statusdevices--name"></span>`name` | string | yes | The device's name. |
| <span id="statusdevices--phase"></span>`phase` | string | no | The device's `status.phase`. One of: `Inventory`, `Idle`, `Starting`, `Connecting`, `Connected`, `Disconnecting`, `Error`. |

### status.display

What the printer columns show.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdisplay--guider"></span>`guider` | string | yes | The `Guider`'s phase, and PHD2's state while the operator reads it, such as `Ready, Guiding`. It is an empty string for a telescope with no `Guider`. |

### status.guider

The telescope's `Guider`, and whether it is ready.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusguider--name"></span>`name` | string | yes | The guider's name. |
| <span id="statusguider--ready"></span>`ready` | boolean | yes | Whether the guider's `Ready` condition is `True`. |
| <span id="statusguider--reason"></span>`reason` | string | no | The reason of the guider's `Ready` condition. |
| <span id="statusguider--phase"></span>`phase` | string | no | The `Guider`'s `status.phase`. One of: `Idle`, `Activating`, `Ready`, `Deactivating`, `Error`. |
| <span id="statusguider--state"></span>`state` | string | no | PHD2's state, the `Guider`'s `status.state`. One of: `Stopped`, `Selected`, `Calibrating`, `Guiding`, `LostLock`, `Paused`, `Looping`. |
