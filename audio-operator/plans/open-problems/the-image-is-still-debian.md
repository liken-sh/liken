# The image is still Debian

Open problem. This operator's image is `debian:stable-slim` with
`pipewire`, `pipewire-bin`, and `wireplumber` installed from the
distribution. The two sibling operators build their images from a
named set of files instead.

## What the siblings did

The bluetooth operator went from 149 MB to 20.3 MB, across two images
that are both `FROM scratch`: 12 MB for the operator's static Go
binary, and 8 MB for BlueZ, dbus, and their configuration, linked
statically against musl. Neither image holds a shell, a package
manager, or a shared object.

The display operator went from 608 MB to 252 MB with a library
closure. Debian ships every libweston backend in one package, so
installing `weston` also installs FreeRDP, neatvnc, GStreamer,
PipeWire, libavcodec, and a speech synthesiser, none of which that
operator loads. `weston-closure.sh` takes the four modules it does
load, resolves what the loader needs for each, and copies that set.

## Which shape this work has

This is the display operator's problem, not the bluetooth operator's.
PipeWire and WirePlumber both open their modules with `dlopen`, so a
static binary carries neither daemon's function. The work is a closure:
name every module each daemon opens by file name, resolve each one's
libraries, and copy that set into an image built from nothing else. A
module that only the running daemon names is a module `ldd` does not
report, which is the part of the display operator's script that took
the reading.

Nobody has written that list for PipeWire and WirePlumber.

## The D-Bus bus is gone

The pod ran a private D-Bus system bus beside the two daemons. Two
reasons put it there, and only one was ever live. With the ALSA monitor
disabled, WirePlumber's device reservation does not run, so PipeWire's
RTKit lookup was the only remaining reason for the bus. Nothing answers
RTKit in this pod, and PipeWire runs without a real-time priority
either way, so the bus did nothing.

The bus is removed. The image no longer installs `dbus`, the operator
is the container's entrypoint and starts the two daemons itself, and
the deleted `entrypoint.sh` took its bus start and its wait loop with
it. That subtracts one daemon from the image. The closure below is what
the image still owes.
