# 21, A person in the procedure

Built on 2026-10-12, and drilled on the QEMU dev cluster on
2026-10-12: a confirmation with the plugin, a weather station turned
unsafe during `Requires`, an expiry at a near `--until`, and a
`notify` answered with `take-down`. "The build" records the decisions
the build made where the design left them open, and where it departs
from the design. "The drill" records the results and the fix to
`AttentionNeeded` that the drill found.

Proposed on 2026-10-12, and designed on 2026-10-12. The design below
replaces three of the first decisions. The wait is not a procedure
action, because a procedure runs in the `Activation` step, after
`PowerOn`, and so cannot keep the power off. A person confirms with a
condition and not an annotation. Any resource can be confirmed, not
only the `Telescope`.

## The problem

Activation assumes the telescope is ready to power on. A portable
telescope spends the day in a garage, and someone has to carry it out,
uncover it, and polar align it before a night. Once
`astrophotography-operator` creates reservations by itself, nothing
must power on and slew a telescope that is still covered. At the end
of a night, someone has to cover it again.

## Decisions

- **The wait is a procedure step, not a new resource.** Plan 13 lets
  each site compose its own procedures from building blocks, as Ekos
  and N.I.N.A. do, and a step that waits for a person is one more
  block. A portable telescope's activation starts with a step such as
  "Set up and polar align", before `PowerOn`, so nothing powers on and
  no INDI process starts until a person confirms. A telescope on a
  fixed pier has no such step.
- **A person confirms with an annotation**, the same mechanism as the
  retry annotation, so plain `kubectl` works, and a `kubectl liken`
  command can wrap it. Anything that can set an annotation can confirm.
- **The confirmation is recorded on the `Telescope` and lasts until
  dawn.** Being set up is a fact about the hardware, not about one
  reservation. When clouds end one reservation and the sky clears for
  a second, the second skips the step instead of waking the person.
  The confirmation expires by itself, so nobody has to remember to
  withdraw it in the morning.
- **A reservation still waiting at dawn ends with nothing powered on,**
  and its status says why.
- **Deactivation's matching step notifies and never waits.** A step
  such as "Cover the telescope" posts an `Event` and sets a condition.
  Parking the mount and warming the camera never wait on a person.
- **The signal to a person is a condition.** Conditions on the
  operator's resources say what waits on a person, and an alert on
  the metric of [plan 22](22-prometheus-metrics.md) reaches their
  phone through their own Alertmanager.
- **A `Telescope` states whether `astrophotography-operator` may
  reserve it unattended at all.** It is off unless the person turns it
  on. A target says what a person wants imaged; this flag says the
  operator may power equipment to do it.

## Considered and set aside

- **A `Hold` resource, like a node's cordon.** It would need its own
  expiry, and the word collides with the `Reservation`'s holder and
  with plan 17's held server.
- **A state on the `Reservation`.** The confirmation would end with
  each reservation, so a second reservation in one night would ask
  again.
- **A standing permission object.** The targets already state the
  demand, and twilight bounds the hours, so the `Telescope`'s flag is
  all the permission needs.

## The design

- **The gate is `spec.requires`, before `PowerOn`.** Any resource of
  the telescope's session, the `Observatory`, the `Telescope`, and each
  device, can list conditions in `spec.requires`, in the form that plan
  13's `requires` already has: a kind, a name, and a condition type.
  The reservation's `Wait` step holds activation until every listed
  condition is `True`, so nothing powers on and no INDI process starts
  before then. This is the pattern of a pod's `spec.readinessGates`. A
  site can list a person's confirmation, and it can also list a
  `WeatherStation`'s `Safe`, so nothing powers on in the rain.
- **`SetUp` is a condition that a person writes**, on any resource: a
  telescope carried out and polar aligned, a camera's dew strap fitted,
  a roof pushed open by hand. A person writes it into the resource's
  `status.conditions` through the status subresource, as an outside
  controller writes a pod's readiness gate. A confirmation is an
  observation of the world, so it belongs in status, where a GitOps
  tool that applies the spec never removes it, and RBAC can grant a
  person the status of a resource without its spec.
- **The `liken` CLI writes it**, so nobody types a status patch by hand.
  `kubectl liken observatory` is a plugin built from this component,
  the way `bluetooth-operator` builds `kubectl liken bluetooth`. Its
  command names the resource, and takes an optional `--until`.
- **The confirmation expires at dawn** at the observatory's
  coordinates, unless `--until` gives another time. The operator then
  sets the condition `False` with the reason `Expired`. The message
  says who confirmed it, and the status write's `managedFields` record
  the user too.
- **The operator keeps a condition it does not own.** Its status writer
  composes the status of each resource, and it must leave `SetUp` as
  the person wrote it, until the expiry.
- **A reservation still waiting at dawn ends with nothing powered on**,
  and its status names each condition it waits for.
- **Deactivation gets a `notify` action** in the procedure vocabulary.
  It posts an `Event` and sets a condition, such as a request to cover
  the telescope, and it never waits.

## Considered and set aside, in the design

- **An annotation**, like the retry annotation. It has no schema, it
  reads as a command and not as a fact, a GitOps tool can remove it as
  drift, and the operator would delete it to expire it.
- **A spec field.** The spec is the desired state, a confirmation is an
  observation, and a GitOps tool sets the spec back to the fleet
  repository's.

## The build

Built on 2026-10-12, against the fake API server and the fake INDI
servers. These are the decisions it made.

- **Two gates, not one.** The design put every requirement in the
  `Wait` step. A telescope that requires a `WeatherStation`'s `Safe`
  would then wait for good, because the station runs on the
  observatory's server, which starts at `StartSite`, after `Wait`. So
  the `Observatory`'s requirements, and those of the devices on its
  server, hold `Wait`, and a new step, `Requires`, between `StartSite`
  and `PowerOn`, holds the `Telescope`'s and its devices'. The `Wait`
  gate comes before the reservation takes its telescope, so a
  reservation that waits for the observatory holds nothing, and its
  telescope stays `Idle`. `Requires` holds the telescope: the
  observatory's server runs and nothing of the telescope does. A
  requirement of the `Observatory` that names a device on its own
  server still waits for good, and the step's message says what it
  waits for; the manual says to put such a requirement on the
  `Telescope`. `Requires` is `Skipped` when nothing is required, and
  a record that an older operator wrote, with no `Requires` step, runs
  the steps it lists. The activation-duration metric leaves out the
  time `Requires` waited.
- **A requirement gates only the start.** A condition that turns
  `False` after `PowerOn` changes nothing, and a `SetUp` that expires
  during a session ends nothing. Plan 13 gives that work to triggers.
- **Dawn is astronomical dawn**, the sun rising through 18 degrees
  below the horizon at the `Observatory`'s `spec.location`, because
  that ends a dark night; civil or nautical dawn would hold a
  confirmation into a sky already too bright to image. `dawn.go`
  computes the sun's altitude from the low-precision formulas of the
  Astronomical Almanac, with no new dependency, scans in steps of five
  minutes, and bisects to the second. Its tests hold the declination
  to the US Naval Observatory's instants of the 2026 equinoxes and
  solstices within 0.02 degrees, London's published sunrise on both
  solstices and the geometric dawn on the equator at the March equinox
  within two minutes, and the altitude to a second formulation,
  Spencer's series as NOAA uses it, within half a degree. A night with
  no astronomical dawn ends at the sun's lowest point in a white night
  and at its highest in a polar night.
- **A reservation that waits at dawn ends.** The gate's step is
  `Skipped` with `Dawn came at <time> while waiting for <conditions>,
  so the session did not start`, and the reservation deactivates:
  `Wait` releases at once, and `Requires` runs the deactivation steps,
  whose `StopSite` stops what `StartSite` started. The `Deactivating`
  or `Released` Event carries the same text. The wait of `Wait` starts
  at the later of the step's start and `spec.start`, and that of
  `Requires` at the step's start.
- **`--until` is `status.setUpUntil`**, a date-time beside the
  condition on each kind with procedures. A `metav1.Condition` has no
  field for a time, an annotation was set aside in the design, and a
  time in the message would be text for the operator to parse. A
  person writes it with `SetUp` in one server-side apply, and a later
  apply with no `--until` removes it, because the plugin is its
  manager. With no `setUpUntil`, `SetUp` expires at the first dawn
  after its `lastTransitionTime` at the resource's `Observatory`. A
  resource with no `Observatory`, such as a device on the shelf, never
  expires until it gets one, and then expires at the dawn after the
  confirmation there.
- **The status writer keeps `SetUp` on every kind.** `mergeConditions`
  keeps a stored `SetUp` that the composed status leaves out, so the
  reservations, the trains, the tubes, and the guiders keep one too.
  The writer copies `status.setUpUntil` from the stored copy, inside
  the write's retry after a `409`, so a person's write between the read
  and the write survives. The expiry is a clock, so the writer keeps a
  timer for the next one; no watch event marks it.
- **The plugin moves `lastTransitionTime` when the status or the
  reason changes**, not on the status alone as `metav1.Condition`
  states. A take-down after an expiry changes `Expired` to `TakenDown`
  with no change of status, and the new time is what tells the
  operator that a person answered a `notify`. A second `set-up` in one
  night keeps the first one's time, and so its dawn.
- **Who confirmed.** The message names the person: `--by` when given,
  else the user that a `SelfSubjectReview` reports, as `kubectl auth
  whoami` reads it, else the kubeconfig's user. The design said that
  `managedFields` record the user too. They do not: they record the
  field manager, `kubectl-liken-observatory`, and the API server's
  audit log is what records the user.
- **The plugin writes by server-side apply, with `force`.** The CRDs
  list the conditions as a map keyed by type, so the apply touches
  `SetUp` and `setUpUntil` alone; a merge patch would replace the whole
  list. `force` takes the condition back from the operator, which
  becomes a manager of its fields when it writes an expiry. A person
  needs `get` on the resource and `patch` on its status, and the
  guide gives that `Role`. The operator's `ClusterRole` needs no
  change: it already has `update` on every status it writes. The
  plugin follows `bluetooth-operator`'s, in the form of
  `library-operator`'s, whose operator is a `Deployment` too: a
  `Deployment` labeled
  `cli.liken.sh/plugin: observatory`, the image
  `observatory-operator-cli` from `Dockerfile.cli`, and `observatory`
  in `liken`'s list of plugin domains for completion. The Claude Code
  catalog in `liken-sh/plugins` lists skills, not `kubectl` plugins,
  and needed no change.
- **`notify` is a field of every action**, beside `job`, and each
  kind's CEL rule accepts exactly one of its actions, `job`, and
  `notify`. The action is `Done` with the summary `Asked: <text>`, and
  the status writer derives `AttentionNeeded` from the run records:
  `True` with the reason `Notified` and each open request as its
  message, `False` with `Activated` once a later activation run begins,
  and `False` with `Acknowledged` once a person's `SetUp` moves after
  the request. The operator's own expiry is no answer. Clearing on the
  next activation alone would leave the alert on all day after the
  person covered the telescope, and `take-down` is the person saying
  so. The transition to `True` is the `Warning` Event; the run's own
  `ProcedureDone` Event carries the text too.
- **The example is unchanged.** Most tests reserve the example's
  `east` telescope, and a `SetUp` requirement there would hold every
  one of them.

## The drill

The drill ran on 2026-10-12 on the QEMU dev cluster, on one node,
`node-1`, with 4 GB of memory. The operator was built from 01411eab and
reached the lab through the lab registry as
`ghcr.io/liken-sh/observatory-operator:dev`. The node held an older
`:dev`, and the `Deployment`'s `IfNotPresent` kept it, so the drill
pinned the `Deployment` to the pushed digest. The observatory was the
example in the namespace `observatory`, with `Guider east`, and the
`Observatory` `lab` at 30.169° S, 70.806° W. Each reservation of `east`
named the outbox `frames`, a `local-path` claim. The plugin was built
with `go build` from `cli/`, and `kubectl` ran it from `PATH` as
`kubectl-liken-observatory`.

| Check | Result |
|---|---|
| Add `{kind: Telescope, name: east, type: SetUp}` to `spec.requires` of `Telescope east`, and reserve `east` | The reservation reached `Requires` 5 s after creation, with the message `Waiting for Telescope east SetUp=True`. `lab-observatory`, `lab-dome`, and `lab-weatherstation` ran, and no pod of `east` existed. |
| `kubectl liken observatory set-up telescope east -n observatory` | The plugin printed `Telescope east is set up until the next astronomical dawn at its Observatory`. `SetUp` was `True` with the reason `SetUp` and the message `Set up by admin`, the user that `kubectl auth whoami` reported. |
| Read `managedFields` of the status | `kubectl-liken-observatory`, operation `Apply`, owned the condition `SetUp` and nothing else. The operator's `Update` owned `Active`, `HorizonFound`, `ParentFound`, and `Ready`, and each kept its value. So the apply merged by type. |
| The reservation after the set-up | `Requires` was `Done` in the second of the apply, with `Found Telescope east SetUp=True`. The reservation was `Ready` 35 s later, after `StartGuider` connected PHD2 in 5 s. |
| Delete the operator pod, and edit the `Telescope`'s spec to raise its generation | The new pod held the `Lease` within 1 s. It wrote the status of generation 3, and `SetUp` kept its status, its message, and its time. |
| `set-up --until 3m` | `status.setUpUntil` was `2026-10-12T23:39:49Z`. 1 s after that time, `SetUp` was `False`, reason `Expired`, message `Expired at 2026-10-12T23:39:49Z: set up by admin`, with one `Expired` `Event`. The operator's `Update` then owned `SetUp` too. |
| Reserve `east` after the expiry | The reservation waited in `Requires` with `Waiting for Telescope east SetUp=True`, and no pod of `east` existed. |
| `set-up` again, with no `--until` | The forced apply took `SetUp` back: the plugin's entry owned it, and the operator's did not. `status.setUpUntil` was gone. |
| `take-down telescope east` after a `notify` | `SetUp` was `False`, reason `TakenDown`, message `Taken down by admin`. `AttentionNeeded` turned `False` with `Acknowledged` 1 s later, with one `Event`. |
| Require `{kind: WeatherStation, name: lab, type: Safe}` on the `Telescope` while a reservation waits, and set the simulator's gust to 40 | `Safe` turned `False`, reason `Danger`. The message was `Waiting for Telescope east SetUp=True, WeatherStation lab Safe=True`, and after a `set-up`, `Waiting for WeatherStation lab Safe=True`, with no pod of `east`. |
| Set the gust to 0 | `Safe` turned `True`, and `Requires` was `Done` in the same second. The reservation was `Ready` 34 s later. Its `Ready` `Event` said `Ready in 41 s` for a reservation 82 s old, so the 42 s that `Requires` waited were left out. |
| Add `notify: Cover the telescope and carry it in` to the `Telescope`'s `deactivation`, and end the reservation with `spec.end` | `Deactivation` took 62 s. The camera warmed for 30 s and the mount parked for 15 s, and then the notify ran in 0 s, beside the dome's park. `AttentionNeeded` turned `True`, reason `Notified`, with the request as its message, and one `Warning` `Event` `Notified`. The reservation was `Released` 67 s after its end. |
| Reserve `east` again, with `SetUp` still `True` from before the request | `AttentionNeeded` stayed `True` after the reservation was `Ready`. The fix follows this table. With the fix, it turned `False`, reason `Activated`, with one `Event`. |
| End that reservation | `AttentionNeeded` stayed `False` for the 46 s between the start of `Deactivation` and the notify, and then turned `True` with a new `Warning` `Event`. |
| Grant the guide's `Role` to a `ServiceAccount`, and run the plugin with a kubeconfig of its token | `set-up mount east` printed `reading the operator version: deployments.apps is forbidden: ...` on stderr and wrote `SetUp` with `Set up by system:serviceaccount:observatory:drill-person`. A `kubectl patch` of the `Mount`'s spec, and `set-up camera east-main`, were `Forbidden`. |

The default expiry is at astronomical dawn, and nothing that the
operator writes gives its time: `status.setUpUntil` is set only by
`--until`, and the plugin prints "the next astronomical dawn" with no
time. So the drill ran `dawnAfter` from `dawn.go` on its own, for the
lab's coordinates after 2026-10-12T23:17Z, and compared the answer to a
script of the NOAA solar calculator's formulas, which follow Meeus and
share no code with `dawn.go`. `dawn.go` answered
`2026-10-13T08:44:52Z`, and the script `08:44:53Z`, which is 05:44 at
the site in UTC-3.

`attention` found the start of an activation only in the resource's
own activation run, and a `Telescope` whose only procedure is the
`notify` of its deactivation has no such run. The guide's example has
that shape, so its `AttentionNeeded` stayed `True` until a person
answered, and never cleared at the next activation as the guide says.
The fix passes `attention` the start of the activity of the resource's
`Telescope` or `Observatory`. That start is gone once the activity
ends, so a request older than the condition's last turn to `False`
stays closed. Without that rule, the deactivation would open the old
request again before its own notify ran. A test of the world and three
cases of `attention`'s table failed before the fix. The operator built
with the fix cleared the condition in the lab, as the table gives.

Two follow-ups of the same day ran on the same reservations.

- **`EditsPending`.** On a `Ready` reservation, a change of
  `spec.outbox.claimName` from `frames` to `frames-2` turned the
  condition `True` in the same second, reason `EditPending`, message
  `spec.outbox.claimName changes from frames to frames-2 at the next
  session`. Changing it back turned it `False`, reason `NoEditPending`,
  10 s later. Each transition posted one `Event`, and the session's
  `PowerOn` had posted the first `NoEditPending`.
- **`observedGeneration` on a `Released` reservation.** An edit of
  `spec.outbox` on the `Released` reservation raised its generation
  from 4 to 5, and within 4 s `status.observedGeneration` and each
  condition's were 5. The reservation had no `EditsPending`, because
  it had no session.

The plugin takes `--kubeconfig`, `--context`, and `--namespace`, and
no other flag of `kubectl`'s, so `--as` failed with `unknown flag`.

## Still open

The flag on the `Telescope` that allows unattended reservations,
which `astrophotography-operator`'s plan 05 needs, waits for that
plan.

The design's alert on a metric of plan 22 has no metric yet:
`AttentionNeeded` is a condition and a `Warning` Event, and no series
of plan 22 carries it.
