# Darkening respects the attached input

A monitor with several inputs dims all of them at once: brightness
and power are panel-global. On 2026-08-27 the lab's ultrawide dimmed
while it showed a laptop on `DP-1`, because the idle policy for the
screen this machine drives on `HDMI-2` ran out its quiet window, and
nothing in the system said the panel was busy being someone else's
monitor. This plan adds the missing fact, `spec.attachedInput`, and
defers every darkening override while the panel shows another input.

## The problem

The override chain works on panel truth: the sidecar states a
desire, the media layer writes `spec.override`, and the operator
darkens the panel. Every link is correct, and the sum is wrong on a
shared monitor, because "this unit is idle" is not "this panel shows
nothing anyone watches". The panel knows what it shows, and the
operator reads it, `status.observed.input`, fresh within the poll's
ten seconds. The missing fact is which input is this machine's own
cable. DDC/CI cannot say: it reports what the panel shows, never
who is asking, and an input switch fires no event on the source's
side. For an HDMI cable the EDID can: CEC routing requires a sink
to serve each port an EDID whose HDMI vendor block carries that
port's physical address, `2.0.0.0` for HDMI input 2, and the
operator already reads the EDID. Both lab panels serve honest
addresses: the ultrawide answers `2.0.0.0` on this machine's
cable, its HDMI 2, and the portable answers `1.0.0.0`. What the
EDID cannot cover, DisplayPort cables and panels that serve the
same address on every port, the owner declares.

Plan 17 never met this failure only because the ultrawide refused
DDC/CI then. The Display made the panel reachable, so the gap
surfaced the first afternoon it could.

## The design

The attached input has two sources, in order:

- `spec.attachedInput`, the owner's declaration, one of
  `status.capabilities.input.values`. It is a declared fact, not a
  desire: the operator never writes it to the panel, which is what
  separates it from `spec.input`, the resting declaration that
  forces the panel to show an input.
- The EDID's physical address, for an HDMI connector whose vendor
  block serves one in the form `N.0.0.0`, mapped to the `HDMI-N`
  input name when the capability list carries it. The operator
  publishes what it derived as `status.attachedInput`, so a person
  can check the derivation against the cabling, and a declaration
  always wins over it.

The guard: when an attached input is known from either source, the
operator obeys a darkening override, `backlight` or `power`, only
after a fresh read of the shown input answers and matches it. The
read must be fresh because the last observed value can be a fossil:
the lab's ultrawide answers the input query with an invalid
response while it shows another source, a failed poll keeps the
last value by design, and a guard that trusted `observed.input`
darkened the panel over the other machine's picture. A read that
fails defers, because a panel that cannot say what it shows is
treated as showing someone else. Otherwise the override stands in
the spec, unactuated, and the operator logs the deferral once. When
the panel returns to the attached input, the next pass's read
answers, and the deferred override obeys then, capture first as
always. The idle policy still works on a shared panel; it waits for
the panel to actually be ours.

Lifts always obey. A restore writes back what the capture saved,
and the capture only ever ran while the panel showed the declared
input, so a lift can never surprise another input's viewer with a
value the panel did not hold.

A panel with no declaration behaves exactly as today, which keeps
the single-input panels out of this entirely.

`spec.input` and `spec.attachedInput` compose but rarely should: a
resting `spec.input` on a shared panel writes the panel back to
this machine within a poll window of every switch away. The field
descriptions carry that warning.

## What was considered and set aside

**Deriving the attached input from DDC or link events.** Set aside
because those signals cannot say it: the connector stays connected
and driven while the panel shows another source, DDC answers
regardless of the shown input, and correlating input changes with
link flaps guesses. The EDID's physical address is adopted instead,
because it is the one channel where the sink names the port itself,
and the guard falls back to the owner's declaration where the EDID
serves none.

**The guard in the media layer.** Set aside because the media
operator would need panel facts, the shown input and the input
vocabulary, that are the display layer's to know. The media layer
keeps stating an honest desire; the display layer knows when the
panel is in a state to obey it.

**Skipping darkening on any multi-input panel without a
declaration.** Set aside because the operator cannot tell a shared
panel from a panel whose other inputs are dead cables, and refusing
to blank every multi-input monitor would break the common case to
protect the shared one.

## How the work is proved

On `liken-1`, with no declaration on either `Display`:

- Both resources report a derived `status.attachedInput`: `HDMI-2`
  on the ultrawide, `HDMI-1` on the portable, matching the cabling
  by eye.
- With the ultrawide showing `DP-1`, the unit's off window runs out:
  the override stands in the spec, the deferral is logged, and the
  panel's brightness does not move.
- Switching the panel to `HDMI-2` lands the deferred blank within
  about a poll window, capture first.
- A wake lifts the override and the panel restores, as drilled in
  plan 18.
- The portable panel, with no declaration, blanks exactly as
  before.
