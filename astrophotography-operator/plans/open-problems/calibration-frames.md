# Calibration frames

Open problem. A stack needs dark frames and flat frames, and the
design does not yet say how the operator takes or keeps them. Darks
depend on the camera's temperature, gain, and exposure, so they can
come from a library. Flats depend on the optical train, so they need
the telescope. `observatory-operator` already has the `FlatPanel` and
`DustCap` kinds.
