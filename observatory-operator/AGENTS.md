# Working on observatory-operator

This directory will hold the operator that controls an observatory's
hardware through INDI, and the manifests that run it. It holds only
plans so far.

`plans/00-design.md` is the design, and the `plans/` directory holds the
plans that build it. Code exists only where a plan calls for it. [Root
plan 74](../plans/74-astrophotography.md) records the architecture that
this component and `astrophotography-operator` share, and the
simulator tests that support it.
