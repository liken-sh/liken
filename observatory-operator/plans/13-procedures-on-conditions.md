# 13, Procedures on conditions

Proposed on 2026-10-06. Steps 1 to 4 and step 6 built on 2026-10-06,
and tested against the fake API server and the fake INDI servers.
Step 5, `job`, is not built. The drill on the test cluster has not
run.

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

Every device kind, the `Telescope`, and the `Observatory` get three
fields:

```yaml
kind: Dome
spec:
  observatory: lab
  activation:
  - state: Unparked
    requires:
    - {kind: WeatherStation, name: lab, type: Safe}
  deactivation: [state: Parked]
  triggers:
  - when: {kind: WeatherStation, name: lab, type: Safe, status: "False"}
    run:
    - state: Parked
      after: [{kind: Mount}]
  - when: {kind: WeatherStation, name: lab, type: Safe, for: 20m}
    run: [state: Unparked]
---
kind: Camera
spec:
  opticalTrain: east-imaging
  activation:   [cool: {celsius: -10, within: 0.5}]
  deactivation: [warm: {celsius: 5}]
---
kind: DustCap
spec:
  activation:   [state: Open]
  deactivation: [state: Closed]
```

- **A trigger is a condition.** `when` names a condition: the
  resource's `kind` and `name`, the condition's `type`, its `status`
  (`"True"` unless the field says otherwise), and optionally `for`, how
  long the status must hold. With no `kind`, the condition is the
  resource's own. A `kind` of `Observatory` or `Telescope` with no
  `name` means the resource's own observatory or telescope. A
  reference names a resource with `kind` and `name`, as a
  `scaleTargetRef` or a `roleRef` does, so one shape serves `when`,
  `requires`, and `after`, and one schema serves every CRD. In
  `after`, a `kind` with no `name` means every resource of that kind in
  the observatory, such as `{kind: Mount}`.
  Conditions are level-triggered: an operator that restarts reads the
  current status and acts on it, so a missed transition loses nothing.
  `for` stops a flapping weather station from opening and closing the
  roof each minute, as `for` does in a Prometheus alert rule.
  Kubernetes `Event`s record each transition for a person, and never
  trigger anything, because the API calls them best effort and they
  expire after an hour. A `Reservation` comes and goes, so no
  procedure names one.
- **An action is a target state.** `state: Parked` is safe to run
  twice and safe to run again after an operator restart, which a
  command such as "park" is not. Each action names exactly one of
  `state`, `cool`, `warm`, or `job`, and the CRD of each kind accepts
  only what the kind supports:

  | Kind | Actions |
  |---|---|
  | `Dome`, `Mount` | `state: Parked` or `Unparked` |
  | `DustCap` | `state: Open` or `Closed` |
  | `FlatPanel` | `state: Lit` or `Dark`, the light |
  | `Camera` | `cool: {celsius, within}`, `warm: {celsius, within}` |
  | every kind, `Telescope`, `Observatory` | `job` |

  Each action takes an optional `timeout`, with a default for each
  action. `warm` switches the cooler off at its target, or at its
  timeout wherever the sensor is, because a cooler cannot warm a
  sensor above the air around it, so a warm-up never fails on a cold
  night. `cool` replaces `Camera.spec.temperature`, so one field holds
  the setpoint.
- **`activation` and `deactivation` are the names people write for two
  triggers.** The `Telescope` and the `Observatory` get an `Active`
  condition. A telescope is active from the start of its
  reservation's `Activation` step, after `Configure`, until the start
  of its `Deactivation` step. An observatory is active from the
  `Activation` step of the first reservation in it until the
  `Deactivation` step of the last telescope in it.
  `activation` runs as `Active` turns `True`, and `deactivation` as it
  turns `False`, while every device is still connected.
- **One rule makes a trigger a barrier.** The operator does not take
  the next lifecycle step until every procedure that the last
  lifecycle transition triggered has finished. A new `Activation`
  step after `Configure` runs the observatory's activation procedures,
  when the observatory is not active yet, and then the telescope's,
  and the telescope becomes `Ready` only after them. `StartSite` stays
  a bring-up step, so its deadline still covers only image pulls. A
  new `Deactivation` step after `Abort` runs the telescope's
  deactivation procedures, and then, when every other telescope in
  the observatory has stopped being active and run its deactivation,
  the observatory's, before `Disconnect` starts. Two reservations that
  end together both still hold their telescopes during their
  `Deactivation` steps, so a test of the holders would leave the
  observatory active, and the last telescope to finish ends it after
  every mount parked. A procedure on any
  other trigger, such as the weather, runs with no step waiting on it.
  A step that finds a failed procedure of the same transition, such as
  the observatory's activation that an earlier reservation started,
  runs it again, because its actions are target states.
- **The tree orders the procedures.** Activation runs top down: the
  `Observatory` and its devices, then the `Telescope` and its devices,
  then the devices of each `OpticalTrain`. Deactivation runs bottom
  up. So the dome unparks before the mount, and the mount parks
  before the dome, with no edge declared. Siblings run in parallel,
  and the actions of one resource run in order. An action's `after`
  names other resources whose runs for the same event must finish
  first: the same lifecycle step, or the same transition of the same
  condition for a trigger in `triggers`. A named resource with no such run is
  not waited for. The tree gives no order to such a trigger, so the
  example's dome parks in bad weather `after: [{kind: Mount}]`.
  Without it, the dome's lock refuses the park while a mount is
  unparked, and the run fails with the driver's refusal.
  An `after` that names a resource in a later tier waits until the
  action's timeout, and the failure names both resources.
- **`requires` gates the operator's own actions.** An action can
  require conditions before it runs, and the operator waits for them
  until the action's timeout. `requires` binds only the actions the
  operator runs. A move that a person makes in KStars is stopped only
  by the safety rules that the drivers enforce.
- **A trigger runs once for each transition, while its resource is
  active.** The record of a run holds the transition time of the
  condition it answers. A trigger in `triggers` whose condition holds
  when the resource's activation finishes runs then, so a dome that
  unparks in bad weather with no `requires` parks again at once. When
  the
  resource's deactivation begins, its `triggers` stop.
- **`job` is the escape hatch.** An action can run a container as a
  Kubernetes `Job`, for a dew heater relay, a webhook, or anything the
  vocabulary lacks: `job: {image, command, args, env}`. The `Job`'s
  environment names the observatory, the telescope, the resource, the
  trigger, and the resource's INDI server. The action finishes when
  the `Job` succeeds, and fails when it fails or passes the action's
  timeout.

### What status shows

Each resource with procedures reports its runs in `status.procedures`:
for each trigger, its last run, the transition time it answers, its
state, and each action with its state, its times, and its message. An
operator that restarts resumes a run from that record, and runs again
only the actions that are not `Done`. `Reservation.status.steps`
copies the actions that a lifecycle step waited on into the step's
`actions`, so `kubectl describe reservation` shows the whole
activation in one place.

A device kind gets the conditions that triggers read, while its device
is connected: `Dome` `Parked` and `Open` (the shutter), `Mount`
`Parked` and `Tracking`, `DustCap` `Open`, `FlatPanel` `Lit`, `Camera`
`Cooling`, and `WeatherStation` `Safe`. A disconnected device has none
of them, so a trigger never reads a stale state. A camera with a
cooler and no activation procedure that cools it gets a note in the
`Activation` step's message, so a missing procedure is not silent.

### What the build settled

Step 3 settled these points, which the design above leaves open:

- A tier runs the `Observatory`'s or the `Telescope`'s own procedure
  first, and then its devices' in parallel. Deactivation runs the
  devices first, and then the owner's.
- `requires` lands with step 3, because the example's dome unparks
  only in safe weather.
- A deactivation action on a device that is not connected is
  `Skipped`, as `Secure` skipped such a device: a reservation whose
  activation failed before `Connect` ends with nothing to act on. An
  activation action on such a device fails.
- A telescope that never began its `Activation` step runs no
  deactivation, and its observatory deactivates only when the
  observatory was active.
- The retry annotation starts the `Activation` step again, and the
  telescope keeps the time it turned active, so a run that is `Done`
  stays `Done`, and a run that failed runs again.
- An observatory's `Active` time comes from its stored condition after
  an operator restart. A stored condition that the status writer had
  not written yet is replaced by the earliest activation of an active
  telescope in it.
- The field of the triggers on conditions is `triggers`, not `on`,
  and a `FlatPanel`'s states are `Lit` and `Dark`, not `On` and
  `Off`, because YAML 1.1, which `kubectl` reads through
  `sigs.k8s.io/yaml`, reads a bare `on`, `On`, or `Off` as a boolean.
  `Lit` matches the panel's `Lit` condition.

Step 4 settled these:

- The trigger controller wakes on `structure`, not `changed`, so an
  INDI reading does not wake it. Each change of a run's record or of
  the `Active` state rings `structure`.
- A trigger's run stops, `Skipped` with the reason, when its condition
  changes before the run ends, or when its resource stops being
  active. A run that waits in `after` stops when its own condition
  changes, before it acts after a run that stopped.
- A trigger's run that failed is not run again for the same
  transition. A trigger whose `when` names nothing records one failed
  run with no `since`.

## What this removes

- `Observatory.spec.policies`, all four fields.
- The steps `Prepare` and `Secure`, and the dome part of `StopSite`.
  Their actions move into `examples/simulators.yaml` as procedures.
- `Camera.spec.temperature`, which the `cool` action replaces.
- The branch `dome-unpark-draft`, which this design replaces.

The API is `v1alpha1` and no one outside the test cluster uses it, so
the fields go with no conversion.

## Also in scope

Defects from the drills of plans 11 and 12 on 2026-10-06 are in the same
lifecycle code:

- The operator deletes a leaving device's pod before its driver has
  stopped, in 3 of 5 removals. The driver restarts once and dies 50 to
  75 ms later. The operator waits for the server to report the driver
  gone before it deletes the pod.
  **Fixed on 2026-10-06.** `setDrivers` waits up to 30 s for the
  server's `delProperty` of each leaving driver. `StopDevices` stops
  the drivers the same way, and a server's pod goes before the pods
  of its devices.
- After the server's pod is replaced, PHD2 stays disconnected and the
  `Guider` stays `Activating`. The guider's connection follows the
  server's, as every device's does. **Fixed on 2026-10-06.** The
  runner sends `set_connected` once for each pair of a guider's pod
  and a server's pod, after the camera and the mount connect on the
  new server.
- A second device with the same driver on one server breaks the
  first: both report `Error`, and the first stays `Starting` for 52 s
  after the second leaves. **Fixed on 2026-10-06.** A server runs one
  device for each driver. The device that runs is the one the server
  runs now, else the older resource, else the first by kind and name.
  The other never starts, and reports `Error` with the name of the
  device that runs the driver.
- During a release, the `Observatory` reports `Activating`, "Waiting
  for Dome lab (Disconnecting)", as plan 78 records. **Fixed on
  2026-10-06.** An `Observatory` whose reservations all deactivate
  reports `Deactivating`, with the names of those reservations.

## The order of the work

1. **Remove the flags.** Delete `policies`. The park locks and the
   shutter rule hold whenever a dome exists. The behavior of the
   example does not change.
2. **The device conditions** that triggers read, each posting its
   transitions as `Event`s through `kubernetes/events`.
3. **The engine and the two lifecycle triggers.** `Telescope` and
   `Observatory` `Active`, `activation`, `deactivation`, the
   target-state actions, `after`, `requires`, the tree order, the
   barrier rule, `status.procedures`, and the steps' `actions`.
   `Prepare` and `Secure` go, and the example states their actions as
   procedures. Built on 2026-10-06.
4. **`triggers` and `for`.** The example closes the dome when
   its weather station reports unsafe for any length of time, and
   opens it after 20 minutes of safe weather. Built on 2026-10-06.
5. **`job`.**
6. **The defects** from the drills, under "Also in scope". All four
   were fixed on 2026-10-06.

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
