# QHY cameras on FX3 chips

Open problem. A QHY camera that powers up as a bare Cypress FX3 chip
stays bare on a `liken` machine, because no `Firmware` object loads it.
INDI's `85-qhyccd.rules` names 65 such chips, among them the QHY5III
and QHY163 families, and `deploy/firmware/qhy.yaml` covers only the
FX2 and FX2LP chips.

## What is missing

`indi-qhy` has no loader for FX3 chips. Its `fxload` is Ubuntu's
0.0.20081013-2ubuntu2, which names the chip types `an21`, `fx`,
`fx2`, and `fx2lp`, and refuses `-t fx3` with "illegal
microcontroller type". No other package of the pinned snapshots is
named `fxload`. So `firmware/generate.go` writes no object for a rule
with `-t fx3`. [Plan 23](../completed/23-firmware-before-the-driver.md)
records how the build found this.

Two programs load FX3 chips, and either one needs a build or a
download in the recipe of `indi`:

- libusb's example program `fxload`, which takes other flags than
  Ubuntu's.
- The `fxload` that QHY's own SDK ships.

The loader must accept the device's usbfs path, because
`firmware-operator` gives the pod the device as `$(LIKEN_DEVICE)`, and
it must run as user 1000 under the `restricted` profile.

The firmware files have a gap too. `QHY492.img`, which the FX3 rule
for `1618:c492` names, is not in the package.

## What a fix needs

- A loader for FX3 chips in `indi-qhy`, with its license in the
  image's notices, and a new revision of `indi`.
- `firmware/generate.go` writes an object for each FX3 rule, with the
  loader's flags, and leaves out a rule whose file the image does not
  hold.
- No FX3 camera has been measured. A drill on one, as for the
  QHY5L-II-C, proves the loader, the flags, and the chip's return as
  the camera.
