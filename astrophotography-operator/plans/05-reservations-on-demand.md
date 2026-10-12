# 05, Reservations on demand

Stub. Proposed on 2026-10-12. Not designed.

A person who has declared targets should not have to book the
telescope every clear night. A reservation powers equipment, so it is
the unit the operator creates and ends, the way a cluster autoscaler
adds and removes nodes. When the telescope allows unattended
reservations and some target can be imaged, the operator creates a
reservation. When no target can be imaged for a grace period that the
person sets, it ends the reservation, and deactivation parks the
mount and warms the camera. The grace period keeps a patchy night from
powering the telescope up and down, which costs minutes of cooling
each time.

The telescope's own flag, from `observatory-operator` [plan
21](../../observatory-operator/plans/21-a-person-in-the-procedure.md),
says whether the operator may do this at all. That plan's setup step
holds a portable telescope until a person confirms it is set up, and
the confirmation lasts the night, so a second reservation after clouds
does not ask again. A reservation that a person makes always takes the
telescope first.

To design: when a target counts as able to be imaged, the grace
period, and how the operator yields to a person's reservation.
