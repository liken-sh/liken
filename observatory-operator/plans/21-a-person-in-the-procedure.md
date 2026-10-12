# 21, A person in the procedure

Proposed on 2026-10-12. Not built. This plan records the problem and
the decisions of the design conversation; the step and its fields are
not designed yet.

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

## Still open

The step, the annotation, the record on the `Telescope`, the flag, and
how the operator computes dawn for the expiry.
