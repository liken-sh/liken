---
aliases: [/docs/reference/procedures/]
title: Procedures
weight: 40
---

# Procedures

A procedure is a list of actions that the operator runs on your
equipment when a reservation starts or ends, such as unparking the
dome, cooling the camera, and opening the dust cap at the start, and
the reverse at the end. A trigger is a list of actions that runs each time a condition
changes to a given status while its resource is active, such as
parking everything when the weather turns unsafe.
[Automate the equipment](/docs/guides/automate-the-equipment/) shows
how to write them.

Every device kind, the `Telescope`, and the `Observatory` have these
fields in their spec:

- `activation` runs as the resource's `Telescope` or `Observatory`
  turns `Active`, in the reservation's `Activation` step.
- `deactivation` runs as it stops being `Active`, in the
  `Deactivation` step, while every device is still connected.
- `triggers` lists triggers. Each trigger has a condition in `when`
  and the actions to run in `run`, as [Triggers](#triggers)
  describes.

The same kinds also have `requires`, which runs no action. It lists
the conditions that must be true before a session starts the
resource, such as a person's `SetUp`. [Requirements and
dawn](/docs/concepts/how-a-reservation-runs/#requirements-and-dawn)
describes how `requires` keeps a reservation waiting.

`activation`, `deactivation`, and each trigger's `run` are lists of
actions that run in order. An action describes a target state, so
running it twice is safe. The operator reads what the device reports,
and sends nothing when the device is already in that state. Each kind
accepts only its own actions, and the CRD rejects the others:

| Kind | Action | Default timeout |
|---|---|---|
| `Dome`, `Mount` | `state: Parked` or `state: Unparked` | 10 min |
| `DustCap` | `state: Open` or `state: Closed` | 10 min |
| `FlatPanel` | `state: Lit` or `state: Dark`, the light | 10 min |
| `Camera` | `cool: {celsius, within}`: write the setpoint and wait until the sensor is within `within`, 0.5 °C by default | 20 min |
| `Camera` | `warm: {celsius, within}`: warm the sensor, then switch the cooler off | 10 min |
| every kind, `Telescope`, `Observatory` | `job: {image, command, args, env}`: run a container once as a Kubernetes `Job` | 10 min |
| every kind, `Telescope`, `Observatory` | `notify: <text>`: ask a person to do something, and continue at once | none |

A `notify` is for what no driver can do, such as covering a portable
telescope. The action is `Done` at once, with the summary
`Notified: <text>`, and never waits for the person. The operator then
sets the resource's
`AttentionNeeded` condition to `True`, with the reason `Notified` and
each open request as its message. The transition posts a `Warning`
Event. `AttentionNeeded` becomes `False` in two cases:

- With the reason `Activated`, when the resource's next activation
  begins.
- With the reason `Acknowledged`, when a person writes the resource's
  `SetUp` condition after the request, as
  `kubectl liken observatory take-down` does. The condition's message
  is then the message of that `SetUp`, such as `Taken down by ada`.

When the operator expires `SetUp` at dawn, `AttentionNeeded` does not
change. A resource that never ran a `notify` has no `AttentionNeeded`.

A cooler cannot warm a sensor above the air around it, so on a cold
night a `warm` can reach its timeout before the sensor reaches the
setpoint. The action then still switches the cooler off and does not
fail, and its summary gives the sensor's temperature at the timeout,
such as `warmed Camera east-main to 2.5 °C by the timeout`.

When a camera's driver restarts, the operator sends it the setpoint
of the camera's first `cool` action again. The `Setpoint` column of
`kubectl get cam` shows that value. The `Activation` step's summary
names each camera that has a cooler and no `cool` action in its
activation.

A `job` is for what the other actions cannot do, such as switching a
dew heater's relay or calling a webhook. The operator creates a
`batch/v1` `Job` in the resource's namespace, owned by the resource,
and the action ends when the `Job` ends: `Done` when it succeeds, and
`Failed` when it fails. A failed action's summary contains the exit
code of the `Job`'s container and the end of its termination message,
at most 300 bytes, such as
`Job observatory-lab-activation-a799431dd4 failed: exit code 3: checking the dew heater`.
The container's `terminationMessagePolicy` is `FallbackToLogsOnError`,
so a script that writes no termination message leaves the end of its
log there instead. When no pod reports how its container ended, such
as after `activeDeadlineSeconds`, the summary contains the `Job`'s own
reason and message. The `Job` runs its pod once, with no retry, and
its `activeDeadlineSeconds` is the action's timeout. Kubernetes
deletes the `Job` an hour after it ends, so you can read its logs for
that hour.

The container runs as user 1000 with no capabilities and the
`RuntimeDefault` seccomp profile, on a read-only root filesystem with
a writable `/tmp`, so the pod meets the `restricted` Pod Security
level. The operator adds the variables below to the container's
environment after the action's own `env`, and replaces a variable of
the same name:

| Variable | Value |
|---|---|
| `LIKEN_OBSERVATORY` | the resource's `Observatory`, such as `lab` |
| `LIKEN_TELESCOPE` | the resource's `Telescope`, for a resource of a telescope |
| `LIKEN_RESOURCE` | the resource, such as `Dome/lab` |
| `LIKEN_TRIGGER` | the trigger, such as `activation` or `triggers[0]` |
| `INDI_HOST`, `INDI_PORT` | the resource's INDI server, such as `lab-observatory.observatory.svc` and `7624` |

The `Job`'s name contains the resource, the trigger, and a hash of
the transition and of the action's position in the run, such as
`observatory-lab-activation-a799431dd4`. When the operator restarts
during a run, it finds the `Job` it created and does not create a
second one. When a run starts again, such as a failed activation
after the retry annotation, the operator deletes the `Job` of the
earlier run and creates a new one.

```sh
kubectl get jobs -n observatory -l observatory.liken.sh/role=job
```

Every action also takes these fields:

- `timeout`, a duration such as `20m`, limits how long the action and
  its waits may take. An action that exceeds it fails.
- `requires` lists conditions, as `{kind, name, type, status}`, that
  must hold before the action runs. `status` is `"True"` unless you
  set it. The operator waits for each condition until the timeout, and
  the action's summary says what it waits for, such as
  `Waiting for WeatherStation lab Safe=True`. `requires` applies only
  to the operator's own actions. When a person moves a mount in KStars,
  only the drivers' park locks can stop the move.
- `after` lists resources, as `{kind, name}`, whose runs in the same
  step must end before this action starts. `{kind: Mount}` with no name means every
  `Mount` in the observatory. A resource with no run in the step is not
  waited for.

A reference with no `kind` refers to the resource itself. A `kind` of
`Observatory` or `Telescope` with no `name` refers to the
`Observatory` or `Telescope` that the resource belongs to. A reference
to a resource that does not exist fails the action, and the message
names the field, such as `after[0]: no Mount north`.

Activation runs the resources in tiers: the `Observatory`, then its
devices, then the `Telescope`, then the telescope's own devices, then
the devices of its trains. Deactivation runs the same tiers in
reverse. The runs within one tier run in parallel. So the dome
unparks before the mount and the mount parks before the dome, with no
`after`. An `after` that names a resource in a later tier waits until
the action's timeout, and then the action fails.

The observatory's tiers run once, when the first reservation in it
activates, and a later reservation finds those runs `Done`. The tiers run again at deactivation, when the last telescope
in the observatory ends.

A `Telescope` and an `Observatory` report the condition `Active`. A
telescope is `Active` from the start of its reservation's `Activation`
step until the start of its `Deactivation` step. An observatory is
`Active` from the `Activation` step of the first reservation in it
until the `Deactivation` step of the last one. The `Observatory`'s
`Active` message names the reservations that currently hold
telescopes in it, such as `Active for Reservation west-tonight`, and
a change of holders keeps its `lastTransitionTime`. Each run records
in `since` the `lastTransitionTime` of the `Active` transition that
started it, so a trigger runs once for each transition.

A resource's `status.procedures` records the last run of each
trigger: its `trigger`, `since`, `state`, start and stop times,
`summary`, and each action with its state, times, and summary. The reservation's
`Activation` and `Deactivation` steps copy the actions they waited on
into `status.steps[].actions`, each with its resource, so
`kubectl describe reservation` shows the whole activation. When an
operator restart interrupts a run, the run resumes from its record and
runs each action that is not `Done` again. A failed action fails its
run and the step. The retry annotation runs a failed run again, and a
run that is `Done` stays `Done`. Each run posts an Event on its
resource when it starts and when it ends: `ProcedureStarted`,
`ProcedureDone`, or the Warning `ProcedureFailed`.

```sh
kubectl get dome lab -n observatory -o jsonpath='{.status.procedures}'
kubectl get rsv east-tonight -n observatory \
  -o jsonpath='{.status.steps[?(@.name=="Activation")].actions}'
```

## Triggers

A trigger runs its actions once each time its condition changes to
the status it names, while its resource is active. A resource is
active from the end of its activation run until its deactivation
begins. A resource with no activation is active as soon as its
`Telescope` or `Observatory` is `Active`. `when` is
`{kind, name, type, status, for}`, with the same rules as a reference
in `requires`. Each run records the condition's `lastTransitionTime`
in `since`. When the condition already has that status as the
resource's activation ends, the trigger runs then, so a dome that
unparks in bad weather with no `requires` parks again at once.

`for` delays the run until the status has lasted that long, such as
`for: 20m`. If the status changes before then, the trigger does not
run, as with `for` in a Prometheus alert rule. A trigger whose condition
refers to a resource that does not exist records one `Failed` run and
posts one Warning.

A run's record uses `trigger: triggers[0]` as its key, and its
summary starts with the condition, such as
`When WeatherStation lab Safe=False: parked Dome lab`. The run's
Events include the condition too:
`Procedure triggers[0] (WeatherStation lab Safe=False) started: state: Parked`.

A run that has started continues to its end, whatever its condition
does meanwhile. A weather station that flaps, or that reconnects and
reports `Safe` as `Unknown` for a moment, does not stop a park
halfway. Only the resource's deactivation or the operator's stop ends
a run. Deactivation ends it as `Skipped` with the reason, and a new
copy of the operator resumes a run that the stop interrupted.

After a restart, the operator writes no status until it has read
each device whose phase is `Connected` from its INDI server, or until
30 seconds have passed. So a restart keeps the `lastTransitionTime` of
each condition, and a trigger does not run again for a transition
that it already handled.

A resource runs one procedure at a time, including its activation and
deactivation, so its device never receives two targets at once. A run
that becomes due while another run of the same resource is in progress
is `Pending`, with a summary such as
`When WeatherStation lab Safe=True for 20m: waiting for the run of triggers[0] (WeatherStation lab Safe=False) to end`.
When the other run ends, the waiting run begins only if its condition
still has the same status and transition time. Otherwise its record is
`Skipped`, with a summary such as
`When WeatherStation lab Safe=True for 20m: WeatherStation lab Safe is no longer True, so the run did not begin`.
A waiting run does not begin either when its resource's deactivation
has begun.

The tiers do not order triggers. To order the runs for one
transition, give a trigger's action an `after`. It waits for the runs
of other resources' triggers on the same condition and status. The trigger does
not wait for a resource that has no such trigger or is not active.

```yaml
kind: Dome
spec:
  triggers:
  - when: {kind: WeatherStation, name: lab, type: Safe, status: "False"}
    run:
    - state: Parked
      after: [{kind: Mount}]
  - when: {kind: WeatherStation, name: lab, type: Safe, for: 20m}
    run: [state: Unparked]
```

The [example observatory](/examples/simulators.yaml) declares a whole site this way: the dome
unparks while the weather station reports `Safe`, the mounts unpark,
the cap opens, and the camera cools to -10 °C. When the weather turns
unsafe, the mounts park and then the dome parks, and after 20 minutes
of safe weather the dome unparks again. At the end, the flat panel's
light goes off, the cap closes, the camera warms to 5 °C, the mounts
park, and the dome parks.

## Testing procedures against the simulators

The INDI server images contain `indi_getprop` and `indi_setprop`.
Because you run them through `kubectl exec`, which needs no shell in
the image, you can drive each simulator from your workstation while a
reservation holds a telescope. The observatory's devices run in the
pod `lab-observatory`, and each telescope's devices run in their own
pod, such as `east-telescope`.

The weather simulator sets `Safe` from its weather parameters, and a
strong wind makes it unsafe. The simulator publishes its parameters
every 60 seconds, so the second command makes it publish them now:

```sh
kubectl -n observatory exec lab-observatory -- indi_setprop "Weather Simulator.WEATHER_CONTROL.Wind;Gust=40;30"
kubectl -n observatory exec lab-observatory -- indi_setprop "Weather Simulator.WEATHER_REFRESH.REFRESH=On"
```

The mounts park, and then the dome parks. A calm wind turns the
weather safe again, and the dome unparks after the 20 minutes of the
example's `for`:

```sh
kubectl -n observatory exec lab-observatory -- indi_setprop "Weather Simulator.WEATHER_CONTROL.Wind;Gust=0;0"
kubectl -n observatory exec lab-observatory -- indi_setprop "Weather Simulator.WEATHER_REFRESH.REFRESH=On"
```

You can park a mount by hand the same way, and read a property with
`indi_getprop`:

```sh
kubectl -n observatory exec east-telescope -- indi_setprop "Telescope Simulator.TELESCOPE_PARK.PARK=On"
kubectl -n observatory exec lab-observatory -- indi_getprop "Weather Simulator.SAFETY_STATUS.*"
```

Read the dome's `status.procedures`, as shown earlier, or its
`Event`s, to see each run start and end:

```sh
kubectl get events -n observatory --field-selector involvedObject.kind=Dome,involvedObject.name=lab
```

The simulators keep their park state in memory, so a device whose
pod restarts comes back unparked, with a dome's shutter in the
simulator's starting state. A real driver reads its park state from
the hardware.
