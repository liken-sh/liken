# 07, Calibration frames

Stub. Proposed on 2026-10-12. Not designed.

Stacking needs calibration frames, and taking them is tedious work
that a person forgets. The operator captures and keeps them over time.
Darks depend on the camera's temperature, gain, and exposure, so they
form a library that the operator keeps current. Flats depend on the
optical train as it stands that night, because dust moves and the
camera rotates, so they belong to one reservation and one train.
`observatory-operator` already controls a `FlatPanel` and a `DustCap`.

To design: which frames the operator takes, when it takes them, and
how a stacking tool finds the ones that match a light frame.
