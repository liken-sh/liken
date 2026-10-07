---
title: Procedures
weight: 40
---

This page states exactly how procedures and triggers run. [Automate
the equipment](/docs/guides/automate-the-equipment/) is the guide to
writing them.

Each resource states what the operator does with its equipment, in
three fields of its spec. Every device kind, the `Telescope`, and the
`Observatory` have them:

- `activation` runs as the resource's `Telescope` or `Observatory`
  turns `Active`, in the reservation's `Activation` step.
- `deactivation` runs as it stops being `Active`, in the
  `Deactivation` step, while every device is still connected.
- `triggers` lists triggers. Each one names a condition in `when`,
  and runs its actions in `run`, as [Triggers](#triggers) states.

`activation`, `deactivation`, and each trigger's `run` are lists of
actions that run in order. An action is a target
state, so it is safe to run twice: the operator reads what the device
reports, and sends nothing when the device is there already. Each kind
accepts only the actions it supports, and the CRD refuses the others:

| Kind | Action | Default timeout |
|---|---|---|
| `Dome`, `Mount` | `state: Parked` or `state: Unparked` | 10 min |
| `DustCap` | `state: Open` or `state: Closed` | 10 min |
| `FlatPanel` | `state: Lit` or `state: Dark`, the light | 10 min |
| `Camera` | `cool: {celsius, within}`: write the setpoint and wait until the sensor is within `within`, 0.5 °C by default | 20 min |
| `Camera` | `warm: {celsius, within}`: warm the sensor, then switch the cooler off | 10 min |
| every kind, `Telescope`, `Observatory` | `job: {image, command, args, env}`: run a container once as a Kubernetes `Job` | 10 min |

A `warm` that does not reach its setpoint by its timeout still
switches the cooler off, and notes where the sensor is, because a
cooler cannot warm a sensor above the air around it. The setpoint of a
camera's first `cool` action is also the setpoint that the operator
sends again to a camera whose driver restarts, and the `Setpoint`
column of `kubectl get cam`. The `Activation` step's summary names each
camera whose driver has a cooler and whose activation does not cool
it.

A `job` is for what the other actions lack, such as a dew heater's
relay or a webhook. The operator creates a `batch/v1` `Job` in its own
namespace, owned by the resource, and the action ends when the `Job`
ends: `Done` when it succeeds, and `Failed` when it fails. A failed
action's summary gives the exit code of the `Job`'s container and the
last lines, at most 300 bytes, of its termination message, such as
`Job observatory-lab-activation-a799431dd4 failed: exit code 3: checking the dew heater`.
The container's `terminationMessagePolicy` is `FallbackToLogsOnError`,
so a script that writes no termination message gives the end of its
log. When no pod reports how its container ended, such as after
`activeDeadlineSeconds`, the summary gives the `Job`'s own reason and
message. The `Job` runs its pod once, with no retry, and
its `activeDeadlineSeconds` is the action's timeout. Kubernetes
deletes it an hour after it ends, so its logs stay that long. The
container runs as user 1000 with no capabilities and the
`RuntimeDefault` seccomp profile, on a read-only root filesystem with
a writable `/tmp`, so the pod meets the `restricted` Pod Security
level. The operator adds these variables to its environment, after
the action's own `env`, and its values replace a variable of the same
name:

| Variable | Value |
|---|---|
| `LIKEN_OBSERVATORY` | the resource's `Observatory`, such as `lab` |
| `LIKEN_TELESCOPE` | the resource's `Telescope`, for a resource of a telescope |
| `LIKEN_RESOURCE` | the resource, such as `Dome/lab` |
| `LIKEN_TRIGGER` | the trigger, such as `activation` or `triggers[0]` |
| `INDI_HOST`, `INDI_PORT` | the resource's INDI server, such as `lab-observatory.observatory.svc` and `7624` |

The `Job`'s name holds the resource, the trigger, and a hash of the
transition and of the action's place in the run, such as
`observatory-lab-activation-a799431dd4`. An operator that restarts
during a run finds the `Job` it created, and creates no second one. A
run that runs again, such as a failed activation after the retry
annotation, deletes the `Job` of the earlier run and creates a new
one.

```sh
kubectl get jobs -n observatory -l observatory.liken.sh/role=job
```

Every action also takes these fields:

- `timeout`, a duration such as `20m`, bounds the action and its
  waits. An action that passes it fails.
- `requires` lists conditions, as `{kind, name, type, status}`, that
  must hold before the action runs. `status` is `"True"` unless the
  field says otherwise. The operator waits for each one until the
  timeout, and the action's summary names what it waits for, such as
  `Waiting for WeatherStation lab Safe=True`. `requires` binds only the
  operator's own actions: a move that a person makes in KStars is
  stopped only by the drivers' park locks.
- `after` lists resources, as `{kind, name}`, whose runs for the same
  step must end first. `{kind: Mount}` with no name means every
  `Mount` in the observatory. A resource with no run in the step is not
  waited for.

A reference with no `kind` names the resource itself. A `kind` of
`Observatory` or `Telescope` with no `name` names the resource's own.
A reference that names nothing that exists fails the action, and the
message names the field, such as `after[0]: no Mount north`.

The tree orders the runs. Activation runs the `Observatory` and then
its devices, then the `Telescope` and then its own devices, then the
devices of its trains. Deactivation runs the same tiers in reverse.
The runs of one tier run in parallel. So the dome unparks before the
mount, and the mount parks before the dome, with no `after`. An
`after` that names a resource of a later tier waits until the
action's timeout, and the action then fails. The observatory's tiers
run when the first reservation in it activates, under a lock, and a
second reservation finds those runs `Done`. They run again at
deactivation when the last telescope in the observatory ends.

A `Telescope` and an `Observatory` report the condition `Active`. A
telescope is `Active` from the start of its reservation's `Activation`
step until the start of its `Deactivation` step. An observatory is
`Active` from the `Activation` step of the first reservation in it
until the `Deactivation` step of the last one. The `Observatory`'s
`Active` message names the reservations that hold telescopes in it
now, such as `Active for Reservation west-tonight`, and a change of
holders keeps its `lastTransitionTime`. Each run records the
`lastTransitionTime` of `Active` that it answers, in `since`, so a
trigger runs once for each transition.

A resource's `status.procedures` holds the last run of each trigger:
its `trigger`, `since`, `state`, start and stop times, `summary`, and
each action with its state, times, and summary. The reservation's
`Activation` and `Deactivation` steps copy the actions they waited on
into `status.steps[].actions`, each with its resource, so
`kubectl describe reservation` shows the whole activation. A run that
an operator restart interrupted resumes from the record, and runs
again each action that is not `Done`. A failed action fails its run
and the step, and the retry annotation runs the failed run again,
while a run that is `Done` stays `Done`. Each run posts an Event on its
resource when it starts and when it ends: `ProcedureStarted`,
`ProcedureDone`, or the Warning `ProcedureFailed`.

```sh
kubectl get dome lab -n observatory -o jsonpath='{.status.procedures}'
kubectl get rsv east-tonight -n observatory \
  -o jsonpath='{.status.steps[?(@.name=="Activation")].actions}'
```

## Triggers

A trigger runs its actions once for each transition of its condition
to the status it names, while its resource is active: from the end of
its activation run, or from the start of its `Telescope`'s or
`Observatory`'s activity when it has no activation, until its
deactivation begins. `when` is `{kind, name, type, status, for}`, with
the same rules as a reference in `requires`. Each run records the
condition's `lastTransitionTime` in `since`. A condition that holds
when the resource's activation ends runs the trigger then, so a dome
that unparks in bad weather with no `requires` parks again at once.

`for` delays the run until the status has held that long, such as
`for: 20m`, and a change of the status before then cancels it, as
`for` does in a Prometheus alert rule. A trigger whose condition names
nothing records one `Failed` run and one Warning.

A run's record keeps `trigger: triggers[0]` as its key, and its
summary starts with the condition, such as
`When WeatherStation lab Safe=False: parked Dome lab`. The run's
Events name the condition too:
`Procedure triggers[0] (WeatherStation lab Safe=False) started: state: Parked`.

A run that began runs to its end, whatever its condition does
meanwhile, so a weather station that flaps, or that reconnects and
reports `Safe` as `Unknown` for a moment, does not stop a park
halfway. Only the resource's deactivation, or the operator's stop,
ends a run. The deactivation ends it as `Skipped` with the reason, and
a new copy of the operator resumes a run that the stop interrupted.

A resource runs one procedure at a time, its activation and
deactivation included, so its device never receives two targets at
once. A run that becomes due while another run of the resource goes
on is `Pending`, with a summary such as
`When WeatherStation lab Safe=True for 20m: waiting for the run of triggers[0] (WeatherStation lab Safe=False) to end`.
When that run ends, the
waiting run begins only if its condition still holds with the same
transition time. Otherwise its record is `Skipped`, with a summary
such as
`When WeatherStation lab Safe=True for 20m: WeatherStation lab Safe is no longer True, so the run did not begin`.
A waiting run whose resource's deactivation began does not begin
either.

The tree gives no order to a trigger, so `after` orders the runs of
one transition: in a trigger, `after` waits for the runs of the other
resources' triggers on the same condition and status. A resource with
no such trigger, or one that is not active, is not waited for.

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

The [example observatory](/examples/simulators.yaml) states a whole site this way: the dome
unparks while the weather station reports `Safe`, the mounts unpark,
the cap opens, and the camera cools to -10 °C. When the weather turns
unsafe, the mounts park and then the dome parks, and after 20 minutes
of safe weather the dome unparks again. At the end, the flat panel's
light goes off, the cap closes, the camera warms to 5 °C, the mounts
park, and the dome parks.

## Testing procedures against the simulators

The INDI server images hold `indi_getprop` and `indi_setprop`. They
run through `kubectl exec` with no shell, so a person can drive each
simulator from your desktop while a reservation holds a telescope. The
observatory's devices run on the pod `lab-observatory`, and each
telescope's devices on its own pod, such as `east-telescope`.

The weather simulator decides `Safe` from its readings. A strong wind
turns it unsafe. The simulator publishes its readings each 60 seconds,
so the second command asks it to publish them now:

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

A person parks a mount by hand the same way, and reads a property
with `indi_getprop`:

```sh
kubectl -n observatory exec east-telescope -- indi_setprop "Telescope Simulator.TELESCOPE_PARK.PARK=On"
kubectl -n observatory exec lab-observatory -- indi_getprop "Weather Simulator.SAFETY_STATUS.*"
```

Each resource's `status.procedures` holds the last run of each
trigger, and its `Event`s give each run's start and end:

```sh
kubectl get dome lab -n observatory -o jsonpath='{.status.procedures}'
kubectl get events -n observatory --field-selector involvedObject.kind=Dome,involvedObject.name=lab
```

The simulators keep their park state in the pod. A device whose pod
starts again comes back unparked, and a dome comes back with its
shutter as the simulator starts it. A real driver reads its park
state from the hardware.
