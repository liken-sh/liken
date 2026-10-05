# The observatory-operator design

Stub. The design is not written yet.
[Root plan 74](../../plans/74-astrophotography.md) holds the
architecture: one pod per INDI device with its own DRA claim, one INDI
server that reaches each driver through a `socat` shim, a reconciler
that configures each device when it appears, and PHD2 as a client of
the server. This document will state the resources, the reconciler,
and the images, when that design is settled.
