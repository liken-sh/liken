# 13, Procedures on conditions

Proposed on 2026-10-06. Built on 2026-10-06, all six steps, and
tested against the fake API server and the fake INDI servers. Two
drills on the test cluster ran on 2026-10-06. "What the first drill
found" and "What the second drill found" list their defects and their
fixes.

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
    and opens when the dome unparks. No field changes it. A dome's
    `state` action includes the shutter (see "What the first drill
    found").

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
- A trigger's run that began runs to its end, whatever its condition
  does meanwhile. Only the resource's deactivation, or the operator's
  stop, ends it. Step 4 first stopped a run when its condition
  changed, and that rule changed the same day: a weather station that
  flaps, or that reconnects and reports `Safe` as `Unknown`, would
  cancel a safety park halfway.
- A resource runs one procedure at a time, the lifecycle's runs
  included. A run that becomes due while another run of the resource
  goes on is `Pending` until that run ends. It then begins only if its
  condition still holds with the same transition time, and is
  `Skipped` with the reason otherwise.
- In a trigger's run, `after` waits for the named resources' runs of
  the transition that the run answers. A resource whose run of that
  transition never began before the condition changed is not waited
  for, because that run never begins.
- A trigger's run that failed is not run again for the same
  transition. A trigger whose `when` names nothing records one failed
  run with no `since`.

Step 5 settled these:

- `job` is a field of `ActionBase`, so the action of every kind has
  it, and the test of the procedure schema holds its schema equal in
  every CRD. An action names exactly one of its kind's actions and
  `job`, and a kind with no action of its own accepts only `job`.
- The `Job`'s name is the resource, the trigger, and a hash of the
  resource, the trigger, the transition time, and the action's index
  in the run, because one run can hold two `job` actions. A `Job` of
  that name that is older than the run belongs to an earlier run of
  the same transition, such as a failed activation that the retry
  annotation runs again. The operator deletes it, with the propagation
  policy `Background` so its pod goes too, and creates a new one. An
  operator that stops after it creates a `Job` and before the status
  writer records the run starts a new run on restart, and that run
  replaces the `Job`, so the job can run twice in that window.
- The action's own variables come first in the environment, and a
  variable that the operator sets replaces one of the same name.
- `deploy/namespace.yaml` states no Pod Security level, so a
  cluster's default applies. The `Job`'s container meets
  `restricted`, the strictest: user 1000, no capabilities, no
  privilege escalation, and the `RuntimeDefault` seccomp profile. Its
  root filesystem is read-only with a writable `/tmp`, as every pod of
  the operator's is. The pod carries no `managed-by` label, because
  the operator's pod watch selects by it.
- A deactivation that ends a trigger's run leaves its `Job` running
  until the `Job` ends or its `activeDeadlineSeconds` passes, as it
  leaves a park that a driver runs.

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
  device that runs the driver. During activation that dropped a
  device with no word in the steps, such as the imaging camera, so
  on the same day activation began to refuse such a pair: `StartSite`
  or `PowerOn` fails, and its message names both devices and the
  driver. The choice above remains for a device that joins a running
  server during a `Ready` reservation.
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
5. **`job`.** Built on 2026-10-06.
6. **The defects** from the drills, under "Also in scope". All four
   were fixed on 2026-10-06.

Each step lands with its tests and keeps the coverage gate.

## What the first drill found

The first drill on the two-node test cluster ran on 2026-10-06, and
found these defects. Each fix lands with a test against the fakes.

- **A dome's target state includes its shutter.** The driver acts on
  `DOME_SHUTTER_PARK_POLICY` only when the park state changes. The
  simulator starts unparked with its shutter closed, so
  `state: Unparked` found the dome unparked and left the shutter
  closed. The action now sets `DOME_SHUTTER` after the park move, or
  when no move was needed, when the shutter is not where the rule puts
  it: open after `state: Unparked`, and closed after `state: Parked`.
- **A trigger's run names its condition.** Events and records said
  "Procedure triggers[0] started", which tells a reader nothing. The
  record keeps `trigger: triggers[0]` as its key. Its summary starts
  with the condition, as in "When WeatherStation lab Safe=False:
  parked Dome lab", and the Events name it after the trigger, as in
  "Procedure triggers[0] (WeatherStation lab Safe=False) started:
  state: Parked". Activation and deactivation keep their names.
- **A refusal explains itself.** When the dome's lock refused a park,
  the run failed with "state: Parked: Dome lab: indi: Dome
  Simulator.DOME_PARK is Alert", while the `DomeParkRefused` Warning
  beside it said why. A park or an unpark that fails with Alert while
  its lock holds now fails with the lock's explanation. In a trigger's
  run, a refused dome park adds that `after: [{kind: Mount}]` orders
  the dome's park after the mounts' parks.
- **The retry annotation runs a failed trigger's run again.**
  `observatory.liken.sh/retry` on a `Reservation` runs its failed step
  again, but a trigger's run that failed had no way to run again for
  the same transition. The same annotation on any resource with
  procedures now runs again each `Failed` run of its triggers whose
  condition still holds with the same transition time, with its `Done`
  actions skipped. The operator then removes the annotation. A
  lifecycle run stays retried through its reservation.
- **A failed `Job` says why.** A failed `job` action gave only the
  `Job`'s reason, `BackoffLimitExceeded`. The container's
  `terminationMessagePolicy` is now `FallbackToLogsOnError`. When the
  `Job` fails, the operator reads its pod once, and the summary gives
  the exit code and the last 300 bytes of the termination message.
  The pod has no `managed-by` label, so no watch holds it, and the
  `Job`'s `Failed` condition is the event that starts the one read.
- **The `Observatory`'s `Active` message names who holds it now.**
  After `drill-east` ended while `drill-west` held the observatory, the
  condition still said "Activated by Reservation drill-east". The
  message now names the reservations that hold telescopes in the
  observatory, as "Active for Reservation drill-west", and the
  transition time stays when the holders change.
- **A device being released reports `Disconnecting`, not
  `Starting`.** During a release, devices posted `Starting` "Waiting
  for its driver on east-telescope" between their disconnect and the
  deletion of their pods, and the observatory's devices did the same
  at `StopSite`. A device whose server only a deactivating reservation
  needs is now `Disconnecting` while it is not connected, and `Idle`
  once its pod is gone.
- **Deleting a dome unlocks the mounts at once.** With the dome
  deleted, each mount kept `DOME_LOCKS` and the last relayed park
  state until its driver restarted or the next `Configure`. The lock
  relay's pass now writes `DOME_IGNORED` to each running mount of an
  observatory whose last dome went, and `DOME_LOCKS` when a dome
  comes, and saves the driver's configuration.
- **How to drive the simulators.** The README gained a section on
  testing procedures against the simulators with `indi_setprop` and
  `indi_getprop` through `kubectl exec`. It notes that the simulators
  keep their park state in the pod, so a device whose pod starts again
  comes back unparked.

## What the second drill found

The second drill on the test cluster ran on 2026-10-06, and found
these defects. Each fix lands with a test against the fakes.

- **The record of a run belongs to one object, not one name.** The
  drill deleted `Dome` `lab` during a session and created it again
  with the same name. The operator kept its runs in memory by
  `Dome/lab`, so the new object's `status.procedures` showed the old
  object's runs, and its activation counted as `Done`. The records are
  now kept by the object's UID, and the operator drops the records of
  an object that the stores no longer hold. A status is part of its
  object, so a restart loads each record under the UID of the object
  that holds it. A trigger's run of a deleted object ends.
- **A device that joins an active parent runs its activation.** Since
  plan 11, a device that gets a parent during a `Ready` reservation
  joins the running server. Activation runs as the `Telescope` or the
  `Observatory` turns `Active`, and a device that joined later missed
  that transition. A dust cap added during a session stayed closed,
  and the recreated dome stayed unparked with its shutter closed. Now
  the trigger controller runs the activation of a device that is
  connected, whose governing resource is `Active`, while a reservation
  that governs it is `Ready`, and that has no activation run of the
  current transition. The run answers the same transition, so its
  `since` is the time the parent turned `Active`. No step waits on it,
  its `after` waits for nothing because the step ended every other run
  of the transition, and its failure posts `ProcedureFailed` as a
  trigger's does. Its triggers start when it ends. The controller
  waits for a `Ready` reservation, because during the `Activation`
  step the step runs the device in the tree's order.
- **A device that leaves an active parent runs its deactivation.** A
  device whose spec moves it to the shelf or to another parent while
  its old parent is `Active` runs its deactivation through its driver
  on the old server, and the driver stops after the run ends, also
  after a failure, which posts `ProcedureFailed`. A deleted device
  runs no deactivation: its spec is gone, and to keep it the operator
  would need a finalizer on every device.
- **The retry annotation runs a joining device's failed activation
  again.** While a reservation that governs the device is `Ready`, the
  annotation on the device runs its `Failed` activation of the current
  transition again. A failure in the `Activation` step fails the
  reservation, so that run still retries through the reservation.
- **A list of holders takes the plural.** The `Observatory`'s `Active`
  message read "Active for Reservation drill-east, drill-west". With
  more than one holder it now reads "Active for Reservations
  drill-east, drill-west", and the `Deactivating` message of its
  `Ready` condition follows the same rule.
- **A device that rejoins activates again.** On the test cluster, the
  dust cap `east` left the session and closed, then rejoined and ran
  no activation, because its activation of the same transition was
  `Done`. The simulator starts open in each new pod, so it read open
  anyway; a real cap would have stayed closed. Now a leave drops the
  device's activation record and a rejoin drops its deactivation
  record, so each move runs its procedure. Two runs in the same second
  have the same start time, so the times cannot order them.
- **A mount's `DOME_POLICY` is written once.** The pass that follows the
  domes wrote the mount's policy and saved the driver's configuration
  twice in each activation, and nine times when a device left and
  rejoined, because the server's epoch moves with each property a
  driver defines while the write is on its way. A real driver rewrites
  its configuration file on each save. Now the pass acts only for a
  `Ready` reservation, since `Configure` writes the policy during
  activation, and one write to a mount runs at a time.

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
