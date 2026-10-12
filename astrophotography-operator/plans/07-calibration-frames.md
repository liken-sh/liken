# 07, Calibration frames

Proposed on 2026-10-12. Not built. This plan records the problem and
the decisions of the design conversation; what the operator takes and
when is not designed yet.

## The problem

Stacking needs calibration frames. Darks record the camera's own
signal, and flats record the dust and vignetting of the light path.
Taking them is tedious, and a person forgets, or takes them after the
light path has changed.

## Decisions

- **Capturing and keeping calibration frames is in scope**, over time,
  not only on the night they are needed.
- **Darks form a library.** They depend on the camera's temperature,
  gain, and exposure, not on the night, so the operator keeps a set
  that covers the settings the targets use.
- **Flats belong to one reservation and one optical train.** Dust moves
  and cameras rotate, so a flat is good for the light path as it stood
  that night. "This reservation, this train" groups them, so no session
  resource is needed for this either.
- **The hardware is already there.** `observatory-operator` controls a
  `FlatPanel` and a `DustCap`, and its procedures can run them.

## Still open

Which frames the operator takes, when it takes them, and how a stacking
tool finds the ones that match a light frame.
