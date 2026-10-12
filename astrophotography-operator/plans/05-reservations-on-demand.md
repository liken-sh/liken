# 05, Reservations on demand

Proposed on 2026-10-12. Not built. This plan records the problem and
the decisions of the design conversation; the rules are not designed
yet.

## The problem

A person who has declared targets should not have to book the
telescope by hand every clear night. But a reservation powers
equipment, cools the camera, and can open a roof, so the operator must
not create one without the person's consent, and must not power up and
down all night when clouds come and go.

## Decisions

- **The operator scales reservations the way a cluster autoscaler
  scales nodes.** Targets are the demand, and a reservation is capacity
  that costs something to run. When some target can be imaged now, the
  operator creates a reservation; when none can, it ends it, and
  deactivation parks the mount and warms the camera. Targets switch
  inside one reservation, so a change of target never stops the
  equipment.
- **There is no permanent reservation.** Each reservation is one
  continuous block, at most one night long.
- **Consent is a flag on the `Telescope`**, from [`observatory-operator`
  plan 21](../../observatory-operator/plans/21-a-person-in-the-procedure.md),
  off unless the person turns it on. Declaring a target never powers
  equipment by itself.
- **A portable telescope waits for its person.** Plan 21's setup step
  holds activation until someone confirms the telescope is out and
  aligned, and the confirmation lasts the night, so a second
  reservation after clouds does not ask again.
- **A grace period, which the person sets, guards against flapping.**
  A patchy night would otherwise power the telescope up and down, and a
  cooled camera takes minutes to reach its temperature each time.
- **A person's own reservation always takes the telescope first.**
- **This plan comes after reservations by hand work.** A reservation
  that a person makes is a subset of this, so nothing built before it
  is thrown away.

## Still open

When a target counts as able to be imaged, the grace period's
settings, and how the operator yields to a person's reservation.
