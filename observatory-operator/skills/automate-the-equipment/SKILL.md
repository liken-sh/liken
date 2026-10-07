---
name: automate-the-equipment
description: "Declare what the operator does with the equipment at the start and end of each reservation and when a condition changes: unpark and park the dome and mount, open and close the dust cap, cool and warm the camera, power devices from a switch, park on bad weather, and run a container as a Job. Use when setting up a dome or a roll-off roof, protecting equipment from weather, cooling a camera, or testing procedures against the simulators."
---

This skill is the guide at https://liken.sh/observatory/docs/guides/automate-the-equipment/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

Each resource can say what the operator does with its equipment. You
write that in three fields of its spec:

* `activation` runs when the reservation starts the telescope.
* `deactivation` runs when the reservation ends, while every device is
  still connected.
* `triggers` run when a condition changes during the reservation,
  such as the weather station reporting unsafe weather.

This guide writes the procedures of a typical site: the dome opens
only in safe weather, the mount unparks, the dust cap opens, and the
camera cools. At the end of the night, and when the weather turns, the
operator closes up in the right order.

## Actions

Each field is a list of actions, and the actions run in order. An
action is a target state, not a command. The operator reads what the
device reports and sends nothing when the device is already there, so
an action is safe to run twice, and safe to run again after the
operator restarts.

| Kind | Action | Default timeout |
|---|---|---|
| `Dome`, `Mount` | `state: Parked` or `state: Unparked` | 10 min |
| `DustCap` | `state: Open` or `state: Closed` | 10 min |
| `FlatPanel` | `state: Lit` or `state: Dark` | 10 min |
| `Camera` | `cool: {celsius, within}`: set the cooler, and wait until the sensor is within `within` degrees, 0.5 by default | 20 min |
| `Camera` | `warm: {celsius, within}`: warm the sensor to within `within` degrees of that temperature, then switch the cooler off | 10 min |
| any kind, `Telescope`, `Observatory` | `job`: run a container once, as a Kubernetes `Job` | 10 min |

Each kind accepts only its own actions, and the API server refuses the
others. Every action also takes `timeout`, such as `timeout: 30m`, and
an action that passes its timeout fails.

## Start and end the night

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: Mount
metadata:
  name: east
spec:
  telescope: east
  driver: {name: indi_eqmod_telescope}
  activation:
    - state: Unparked
  deactivation:
    - state: Parked
---
apiVersion: observatory.liken.sh/v1alpha1
kind: DustCap
metadata:
  name: east
spec:
  opticalTrain: east-imaging
  driver: {name: indi_snapcap}
  activation:
    - state: Open
  deactivation:
    - state: Closed
---
apiVersion: observatory.liken.sh/v1alpha1
kind: Camera
metadata:
  name: east-main
spec:
  opticalTrain: east-imaging
  driver: {name: indi_asi_ccd}
  activation:
    - cool: {celsius: -10, within: 0.5}
  deactivation:
    - warm: {celsius: 5}
```

The operator unparks the mount, and it never starts or stops tracking.
Whether a mount tracks after it unparks depends on its driver. You
align and start tracking from KStars.

Warm the camera before the cooler goes off. A sensor that loses its
cooling at -10 °C warms by tens of degrees in seconds, and that stress
can damage it. A cooler cannot warm a sensor above the air around it,
so on a cold night `warm` may not reach its target. In that case it
switches the cooler off at its timeout and notes the temperature it
reached, and it does not fail.

The first `cool` action's temperature is also the setpoint that the
operator sends again if the camera's driver restarts during the night.

## The order of the procedures

The tree orders the procedures, so most setups need no explicit order.
Activation runs from the top of the tree down, in five tiers:

1. the `Observatory`'s own procedures
2. the procedures of the observatory's devices, such as the dome
3. the `Telescope`'s own procedures
4. the procedures of the telescope's own devices, such as the mount
5. the procedures of the devices of its optical trains, such as the
   dust cap and the camera

Each tier starts when the tier before it ends, and the procedures of
one tier run at the same time. Deactivation runs the same tiers from
the bottom up. So the dome opens before the mount unparks, and the
mount parks before the dome closes, with nothing more to write.

When two telescopes share an observatory, the observatory's procedures
run once: when the first reservation starts, and when the last one
ends.

To order two resources of the same group, add `after` to an action. It
lists resources, as `{kind, name}`, whose procedures for the same step
must finish first. `{kind: Mount}` with no name means every `Mount` in
the observatory.

## Wait for a condition

`requires` makes an action wait until conditions hold. This dome opens
only while the weather station reports `Safe`, and waits up to the
action's timeout for it:

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: Dome
metadata:
  name: backyard
spec:
  observatory: backyard
  driver: {name: indi_rolloffino}
  activation:
    - state: Unparked
      requires:
        - {kind: WeatherStation, name: backyard, type: Safe}
  deactivation:
    - state: Parked
```

Each entry names a resource and one of its conditions, and `status`
is `"True"` unless you set it. The action's summary says what it waits
for, such as `Waiting for WeatherStation backyard Safe=True`.

`requires` binds only the operator's own actions. A move that someone
makes in KStars is stopped only by the drivers' park locks.

A dome unparked means open, and parked means closed. While the
observatory has a `Dome`, the operator sets each dome to close its
shutter when it parks and open it when it unparks, so one action moves
both.

## React to the weather

A trigger runs its actions once each time its condition changes to the
status it names. This dome closes as soon as the weather turns unsafe,
and opens again after 20 minutes of safe weather:

```yaml
spec:
  triggers:
    - when: {kind: WeatherStation, name: backyard, type: Safe, status: "False"}
      run:
        - state: Parked
          after: [{kind: Mount}]
    - when: {kind: WeatherStation, name: backyard, type: Safe, for: 20m}
      run:
        - state: Unparked
```

Give each mount its own trigger on the same condition, to park:

```yaml
spec:
  triggers:
    - when: {kind: WeatherStation, name: backyard, type: Safe, status: "False"}
      run:
        - state: Parked
```

Three details make this work:

* A trigger has no tree order, so the dome's park names
  `after: [{kind: Mount}]`. Without it, the dome tries to park while a
  mount is still unparked, and the park lock refuses it.
* `for: 20m` waits until the status has held that long. A change
  before then cancels the run, so a station that flaps between safe and
  unsafe does not open and close the roof every few minutes.
* A run that has started finishes, whatever the condition does
  meanwhile. A brief `Unknown` from a station that reconnects does not
  stop a park halfway.

If the weather is already unsafe when activation ends, the trigger
runs then. The mount stays parked when the weather clears, because
its trigger only parks. You decide when to unpark it again.

The conditions a trigger can name are the state conditions of the
devices, such as `Parked` on a `Mount` or `Safe` on a
`WeatherStation`. [Reading the status](https://liken.sh/observatory/docs/reference/reading-the-status/#state-conditions)
lists them all.

## Power devices from a switch

A device's `power` names the output of a `Switch` that powers it, from
output 1. The operator switches the output on before it starts the
device's pod, and off after the pod stops:

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: Switch
metadata:
  name: east
spec:
  telescope: east
  driver: {name: indi_simulator_io}
---
apiVersion: observatory.liken.sh/v1alpha1
kind: Camera
metadata:
  name: east-main
spec:
  opticalTrain: east-imaging
  driver: {name: indi_asi_ccd}
  power: {switch: east, output: 1}
```

Several devices can name one output, as they do when a USB hub sits on
one 12 V line.

The operator switches an output through INDI's output interface: the
property `DIGITAL_OUTPUT_<n>` of the switch's driver. The simulator
`indi_simulator_io` provides it. Many power boxes have drivers with
power properties of their own and no `DIGITAL_OUTPUT_<n>`, and the
operator cannot switch those. Check the driver's properties in KStars
before you rely on `power`.

## Run a container

A `job` action runs a container once, for anything the other actions
cannot do, such as switching a dew heater's relay or posting to a
webhook. The operator creates a Kubernetes `Job` in the `observatory`
namespace, and the action ends when the `Job` ends:

```yaml
spec:
  activation:
    - job:
        image: curlimages/curl:8.11.1
        command: [curl, -fsS, -d, "roof opening", https://ntfy.example/backyard]
```

The container runs as user 1000 with no capabilities and a read-only
root filesystem, with a writable `/tmp`. The operator adds these
variables to its environment:

| Variable | Value |
|---|---|
| `LIKEN_OBSERVATORY` | the resource's `Observatory`, such as `backyard` |
| `LIKEN_TELESCOPE` | the resource's `Telescope`, for a resource of a telescope |
| `LIKEN_RESOURCE` | the resource, such as `Dome/backyard` |
| `LIKEN_TRIGGER` | the procedure, such as `activation` or `triggers[0]` |
| `INDI_HOST`, `INDI_PORT` | the resource's INDI server |

So a script can drive any INDI property through `INDI_HOST` and
`INDI_PORT`. When the container fails, the action fails, and its
summary gives the exit code and the last lines of the container's
output. Kubernetes deletes the `Job` an hour after it ends.

## Test the procedures with the simulators

Run your procedures against the example observatory from
[Install](https://liken.sh/observatory/docs/guides/install/) before you trust them with real
equipment. The INDI server images hold `indi_getprop` and
`indi_setprop`, so you can drive a simulator through `kubectl exec`.

Turn the simulated weather unsafe with a strong wind. The simulator
publishes its readings every 60 seconds, so the second command asks
for them now:

```sh
kubectl -n observatory exec lab-observatory -- indi_setprop "Weather Simulator.WEATHER_CONTROL.Wind;Gust=40;30"
kubectl -n observatory exec lab-observatory -- indi_setprop "Weather Simulator.WEATHER_REFRESH.REFRESH=On"
```

The mounts park, and then the dome parks. Calm the wind, and the dome
opens again after the trigger's 20 minutes:

```sh
kubectl -n observatory exec lab-observatory -- indi_setprop "Weather Simulator.WEATHER_CONTROL.Wind;Gust=0;0"
kubectl -n observatory exec lab-observatory -- indi_setprop "Weather Simulator.WEATHER_REFRESH.REFRESH=On"
```

Each resource's `status.procedures` holds the last run of each
procedure, with every action and its result. Its Events give each
run's start and end:

```sh
kubectl get dome lab -n observatory -o jsonpath='{.status.procedures}' | jq
kubectl events -n observatory --for dome/lab
```
