# 10, Planetary and solar imaging

Proposed on 2026-10-15, in the design conversation of [plan
04](04-the-scheduler.md), as a stub so that the work is not lost. Not
designed.

## The problem

The operator images deep-sky targets: fixed coordinates and long
guided exposures. The planets, the moon, and the sun need different
work.

- **Planetary imaging is lucky imaging.** The camera records thousands
  of exposures of a few milliseconds as video, and a stacking tool
  keeps the sharpest. The mount tracks at the planet's own rate, and
  PHD2 does not guide.
- **Solar imaging is in daylight, through a solar filter.** The
  twilight filter of [plan 04](04-the-scheduler.md) turns around, and
  safety becomes a rule: the mount never slews near the sun without
  the filter on.
- **A body's position moves.** The sun, the moon, and the planets come
  from formulas, Meeus or VSOP87, with no network. A comet needs
  current orbital elements from the Minor Planet Center.

[Plan 09](09-a-targets-position-by-name.md) gives a `Target` its
position by a body's IAU name, and puts the capture method on the
sequence.

## Still open

Everything: the capture method's fields, tracking rates, the safety
rule for the sun, where the orbital elements of a comet come from, and
how the scheduler weighs a target of minutes against one of hours.
