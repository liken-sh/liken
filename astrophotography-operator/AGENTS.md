# Working on astrophotography-operator

This directory will hold the operator that runs imaging sessions on an
observatory. It holds only plans so far.

`plans/00-design.md` is the design, and the `plans/` directory holds the
plans that build it. Code exists only where a plan calls for it. This
component depends on `observatory-operator`, and `observatory-operator`
does not depend on it. [Root plan 74](../plans/74-astrophotography.md)
records the architecture that the two components share.
