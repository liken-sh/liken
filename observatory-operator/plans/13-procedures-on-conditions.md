# 13, Procedures on conditions

Proposed on 2026-10-06. Not built.

## The problem

The operator decides what an observatory does with its equipment. On
activation, `Prepare` opens every dust cap, cools every camera, and
unparks the mount. On deactivation, `Secure` switches every flat panel
off, closes the caps, parks the mount, and warms the cameras, and
`StopSite` parks the dome. Nobody asked for that order: the operator
invented it, and no field changes it.

Where the operator does take settings, they are flags named after
INDI's switches. `Observatory.spec.policies` has four:
`domeLocksMount`, `mountLocksDome`, `closeShutterOnPark`, and
`openShutterOnUnpark`. A fifth, to unpark the dome on activation, was
drafted on 2026-10-06 and set aside on the branch `dome-unpark-draft`,
and closing the dome on bad weather would have been a sixth. Each new
behavior adds a flag and a hard-coded branch in a step.

The established tools do the opposite. N.I.N.A.'s sequencer runs the
instructions a person puts under a trigger such as "When becomes
unsafe". Ekos runs startup and shutdown procedures that a person
composes from steps such as "Dome - Unpark", plus the person's own
scripts, because each observatory is different. They ship building
blocks, and each site composes its own routine.

## The design

The operator keeps what no site should change, and each resource
states its own procedures.

### What the operator keeps

- **Bring-up and tear-down:** `Wait`, `StartSite`, `PowerOn`,
  `StartDevices`, `Connect`, `Configure`, `StartGuider`, `Abort`,
  `StopGuider`, `Disconnect`, `StopDevices`, `PowerOff`, and the
  server part of `StopSite`. These create pods and connections, write
  the settings from each `spec`, and end the holder's activity.
- **The safety rules, always on:**
  - The park locks hold whenever an observatory has a dome. The mount
    does not unpark while the dome is parked, and the dome does not
    park while any mount is unparked. The operator writes
    `DOME_POLICY` and `MOUNT_POLICY` and relays the park states, as
    plan 12 built. No field turns them off.
  - The shutter follows the park state: it closes when the dome parks,
    and opens when the dome unparks. No field changes it.

### What each resource states

A device kind, and `Observatory` and `Telescope` where it fits, gets
three fields:

```yaml
kind: Dome
spec:
  observatory: lab
  activation:   [state: Unparked]
  deactivation: [state: Parked]
  on:
  - when: {weatherStation: lab-weather, type: Safe, status: "False"}
    run: [state: Parked]
  - when: {weatherStation: lab-weather, type: Safe, status: "True", for: 20m}
    run: [state: Unparked]
---
kind: Camera
spec:
  opticalTrain: east-imaging
  activation:   [cool: {celsius: -10, within: 0.5, timeout: 20m}]
  deactivation: [warm: {celsius: 5, timeout: 10m}]
---
kind: DustCap
spec:
  activation:   [state: Open]
  deactivation: [state: Closed]
```

- **A trigger is a condition.** `when` names a condition on a resource
  in the same observatory: its type, its status, and optionally `for`,
  how long the status must hold. Conditions are level-triggered: an
  operator that restarts reads the current status and acts on it, so a
  missed transition loses nothing. `for` stops a flapping weather
  station from opening and closing the roof each minute, as `for`
  does in a Prometheus alert rule. Kubernetes `Event`s record each
  transition for a person, and never trigger anything, because the
  API calls them best effort and they expire after an hour.
- **A trigger names its resource by its place in the tree, or by
  name.** `telescope:` and `observatory:` with no name mean the
  resource's own ancestors. A `Reservation` comes and goes, so no
  procedure names one.
- **An action is a target state.** `state: Parked` is safe to run
  twice and safe to run again after an operator restart, which a
  command such as "park" is not. `cool` and `warm` name a temperature
  and a tolerance. Each action has a timeout, and the CRD schema
  accepts only the actions a kind supports.
- **`activation` and `deactivation` are the names people write for two
  triggers.** The `Telescope` gets an `Active` condition, `True` while
  a reservation holds it, and the `Observatory` gets one that is
  `True` while any of its telescopes is active. `activation` runs on
  `Active=True`, and `deactivation` on `Active=False`.
- **One rule makes a trigger a barrier.** The operator does not take
  the next lifecycle step until every procedure that the last
  lifecycle transition triggered has finished. The telescope becomes
  `Ready` only after its activation procedures finish, and
  `Disconnect` starts only after its deactivation procedures finish.
  A procedure on any other trigger, such as the weather, runs with no
  step waiting on it.
- **The tree orders the procedures.** Activation runs top down: the
  `Observatory`'s devices, then the `Telescope`'s, then each
  `OpticalTrain`'s. Deactivation runs bottom up. So the dome unparks
  before the mount, and the mount parks before the dome, with no edge
  declared. Siblings run in parallel. `after:` names another
  resource's procedure for an order the tree does not give.
- **`requires` gates the operator's own actions.** An action can
  require a condition before it runs:
  ```yaml
  activation:
  - state: Unparked
    requires: [{dome: lab-dome, type: Open, status: "True"}]
  ```
  The operator waits for the condition until the action's timeout.
  `requires` binds only the actions the operator runs. A move that a
  person makes in KStars is stopped only by the safety rules that the
  drivers enforce.
- **`job` is the escape hatch.** An action can run a container as a
  Kubernetes `Job`, for a dew heater relay, a webhook, or anything the
  vocabulary lacks. The action finishes when the `Job` succeeds.

### What status shows

`Reservation.status.steps` lists each procedure's actions under the
lifecycle step that waits on them, with the resource, the action, its
state, its times, and its message. A device kind gets the conditions
that triggers read, such as `Dome` `Parked` and `Open`, `Mount`
`Parked`, `DustCap` `Open`, `Camera` `AtTemperature`, and
`WeatherStation` `Safe`. A resource with no activation procedure that
usually has one, such as a cooled camera, says so in its status, so a
missing procedure is not silent.

## What this removes

- `Observatory.spec.policies`, all four fields.
- The steps `Prepare` and `Secure`, and the dome part of `StopSite`.
  Their actions move into `examples/simulators.yaml` as procedures.
- The branch `dome-unpark-draft`, which this design replaces.

The API is `v1alpha1` and no one outside the test cluster uses it, so
the fields go with no conversion.

## Also in scope

Two defects from plan 11's drill on 2026-10-06 sit in the same
lifecycle code:

- The operator deletes a leaving device's pod before its driver has
  stopped, in 3 of 5 removals. The driver restarts once and dies 50 to
  75 ms later. The operator waits for the server to report the driver
  gone before it deletes the pod.
- After the server's pod is replaced, PHD2 stays disconnected and the
  `Guider` stays `Activating`. The guider's connection follows the
  server's, as every device's does.

## The order of the work

1. **Remove the flags.** Delete `policies`. The park locks and the
   shutter rule hold whenever a dome exists. The behavior of the
   example does not change.
2. **The conditions.** `Telescope` and `Observatory` `Active`, and the
   device conditions that triggers read, each posting its transitions
   as `Event`s through `kubernetes/events`.
3. **The engine and the two lifecycle triggers.** `activation`,
   `deactivation`, the target-state actions, the tree order, the
   barrier rule, and `status.steps`. `Prepare` and `Secure` go, and
   the example states their actions as procedures.
4. **`on`, `for`, and `requires`.** The example closes the dome when
   its weather station reports unsafe for any length of time, and
   opens it after 20 minutes of safe weather.
5. **`job`.**
6. **The two defects** from plan 11's drill.

Each step lands with its tests and keeps the coverage gate.

## How we test it

Unit tests in synctest bubbles, against the fake API server and the
fake INDI servers. The drill on the two-node test cluster runs the
example's procedures through a reservation of each telescope, turns the
weather simulator unsafe during a session to see the dome park and the
mount refuse to unpark, turns it safe again to see the dome wait 20
minutes and open, and runs a `job` action.

## References

- N.I.N.A., "When becomes unsafe" in the Sequencer Powerups plugin:
  <https://www.backyardastronomy.net/2026/08/30/nina-weather-and-safety-monitoring-protecting-your-equipment-while-you-sleep/>
- Ekos startup and shutdown procedures:
  <https://kstars-docs.kde.org/en/user_manual/ekos-scheduler.html>, and
  the task queue: <https://kstars-docs.kde.org/en/user_manual/ekos-scheduler-taskqueue.html>
- Kubernetes container lifecycle hooks, the precedent for procedures
  in a resource's own spec:
  <https://kubernetes.io/docs/concepts/containers/container-lifecycle-hooks/>
- `for` in Prometheus alerting rules:
  <https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/>
- Root plan 78, on what belongs in a condition and what in an `Event`.
