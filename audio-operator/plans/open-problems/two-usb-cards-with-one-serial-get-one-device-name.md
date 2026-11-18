# Two USB cards with one serial get one device name

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`endpointName` in `audio-operator/names.go` names a USB card that
reports a serial from the machine's name, "usb", the vendor, the
product, the serial, and the PCM id. The file's header says that the
serial tells apart two dongles of one model on one machine, and also
that dongles of one model can all report the same serial. When two
such dongles are on one machine, both get the same name.

`nameEndpoints` names each endpoint on its own and does not check
for a name that is already taken. The duplicate name then reaches
every place that keys by the name:

- `sliceDevices` writes both devices into the `ResourceSlice`. The
  API server's validation refuses a slice with two devices of one
  name, as far as this review knows, so the pass cannot publish any
  device of the machine.
- `endpointInventory.publish` keeps one endpoint for the name, so a
  prepare call for that name reaches one card and not the other.
- Both endpoints write the same `Sink` or `Source`.

## What would confirm it

Two USB audio dongles of one model that report the same serial,
plugged into one machine, and the result of the next reconcile pass.
A unit test of `nameEndpoints` with two such cards shows the
duplicate name without hardware.

## What a fix has to decide

How to name the second card. The port path, which a card with no
serial already uses, tells the two apart, but it changes when the
card moves to another port. A fix that adds the port path only on a
collision keeps the names of every other card, but the name of a card
then depends on which other cards are plugged in.
