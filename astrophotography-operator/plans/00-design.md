# The astrophotography-operator design

Stub. The design is not written yet.
[Root plan 74](../../plans/74-astrophotography.md) holds the
architecture: the operator is a client of `observatory-operator`'s INDI
server that never receives frame data, a planner that chooses one
exposure at a time, and grading and plate solving beside the camera.
This document will state the resources, the planner, and the frame
records, when that design is settled.
