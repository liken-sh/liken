# 13. The attached input retires

## The problem

[Plan 11](completed/11-darkening-respects-the-attached-input.md)
guarded the darkening override with a question to the panel: which
input do you show? The bench work of 2026-08-27 proved the question
worthless on the one panel that ever needed it. The shared monitor
answers VCP `0x60` with the name of the port the question arrived
on, always: while it showed the other machine, while it showed this
one, right after a switch, and minutes after. The read is a mirror,
not a sensor. In other states the same panel answers the query with
a malformed reply, or parks its DDC and answers nothing.

The guard's only customers were shared panels, and a shared panel
is exactly the kind that lies, because its firmware serves several
masters. On a single-input panel the guard never acts, but it still
costs: one flaky answer defers the blank forever, so the guard can
break the panels it was never needed for. Both dims that plan 11
was built to prevent happened anyway, through the mirror answer.

## The design

The attached input is removed entirely: `spec.attachedInput`,
`status.attachedInput`, the printer column, the EDID derivation of
the port from the HDMI vendor block, the decision-time read, and
the deferral machinery. A darkening override actuates whenever the
panel answers, which is the behavior plan 08 shipped.

The decision the guard tried to compute now lives where a person
can state it: the media layer's `Player` carries an idle policy,
and `offAfterSeconds: 0` declares that a screen never goes dark on
its own. A shared screen's owner sets that once, from knowledge no
EDID carries: who else uses the panel. The operator stops guessing.

What this gives up, deliberately: a shared panel whose owner leaves
the dark window on will dim every input after the quiet stretch.
That failure is visible on the glass, recoverable at a button, and
belongs to configuration, where the fix is one field, instead of to
a sensor nobody can trust.

## Considered and set aside

Keeping `status.attachedInput` as an observed fact, with no guard
reading it, was the shallow unwind. The derivation existed to feed
the guard, nothing else consumes it, and a status field nobody acts
on is trivia.

A per-panel declaration that the input answer is untrustworthy kept
the guard for honest panels. No honest multi-input panel is known,
so the knob would ship with every user set to distrust.

Cutting this machine's own video signal, per-connector DPMS through
the compositor, dims nobody else's input by construction and needs
no guard at all. It remains the right shape if a shared screen ever
needs to go dark; nothing needs it today, so nothing carries it.

## What was measured and what was read

The mirror answer was measured at the panel's own buttons on
2026-08-27: four reads of `status.observed.input` against what the
glass showed, every one answering the port this machine occupies,
two of the four false. The malformed-reply state was measured
during plan 11's drill, and the parked-DDC state appears in the
operator's log the same night. The removal itself subtracts code
and proves nothing new on the wire.
