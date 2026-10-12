# 21, A person in the procedure

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
  the metric of [plan 22](completed/22-prometheus-metrics.md) reaches their
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

## Still open

The flag on the `Telescope` that allows unattended reservations,
which `astrophotography-operator`'s plan 05 needs, waits for that
plan.
