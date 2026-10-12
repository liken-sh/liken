# 21, A person in the procedure

Stub. Proposed on 2026-10-12. Not designed.

Activation assumes the telescope is ready to power on. A portable
telescope is in a garage during the day, and someone has to carry it
out, uncover it, and polar align it first. When
`astrophotography-operator` creates reservations on its own, the
operator must not power on and slew a telescope that is still covered.

Plan 13 lets each site compose its own procedures from building blocks.
This plan adds a block that waits for a person. A portable telescope's
activation starts with a step such as "Set up and polar align", before
`PowerOn`, so nothing powers on and no INDI process starts. The
reservation shows the message in its step summary and in a condition.
A person confirms with an annotation, the same mechanism as the retry
annotation, and a `kubectl liken` command can wrap it. A telescope on a
fixed pier has no such step.

The confirmation is a fact about the hardware, so it is recorded on the
`Telescope` and lasts until dawn. A second reservation in the same
night, after clouds ended the first, skips the step. A reservation
that is still waiting at dawn ends with nothing powered on, and says
why.

Deactivation has the matching block, which notifies and never waits: a
step such as "Cover the telescope" posts an `Event` and sets a
condition. Parking the mount and warming the camera never wait on a
person. A cluster owner can alert on the condition through the metrics
of [plan 22](22-prometheus-metrics.md).

A `Telescope` also states whether `astrophotography-operator` may
reserve it unattended at all. It is off unless the person turns it on,
so declaring a target never powers equipment by itself.

To design: the step, the annotation, the record on the `Telescope`,
the flag, and what "dawn" means for the expiry.
