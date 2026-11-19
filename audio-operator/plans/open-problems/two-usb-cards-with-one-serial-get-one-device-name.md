# Two USB cards with one serial get one device name

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced the duplicate name on
2026-11-19.

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

## The reproduction

The test called `nameEndpoints` for `liken-1` with two USB cards of
one vendor and product that report the serial `000000000000`, on the
ports `1-2` and `1-3`. It asserted that the cards get two names, and
it failed with:

    both cards are named "liken-1-usb-0d8c-0014-000000000000-usb-audio", want two names, because a ResourceSlice holds each device name once

The test is not in the tree, because a failing test cannot land on
`main`. The effect on the `ResourceSlice` and the `Sink` needs two
such dongles plugged into one machine, and the next reconcile pass.

## What a fix has to decide

How is the second of two identical dongles named? The port path, which a card with no
serial already uses, tells the two apart, but it changes when the
card moves to another port. A fix that adds the port path only on a
collision keeps the names of every other card, but the name of a card
then depends on which other cards are plugged in.
