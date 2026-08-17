# The image is still Debian

Open problem. This operator's image is `debian:stable-slim` with
`pipewire`, `pipewire-bin`, `wireplumber`, and `dbus` installed from
the distribution. The two sibling operators build their images from a
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

## One finding that may remove a daemon

The pod runs a private D-Bus system bus beside the two daemons. Two
reasons put it there, and only one of them is still live. With the ALSA
monitor disabled, WirePlumber's device reservation no longer runs, so
PipeWire's RTKit lookup is the only remaining reason the pod starts a
bus. Nothing answers RTKit in this pod, and PipeWire runs without a
real-time priority either way.

So the bus may be removable, which takes a whole daemon out of the
image and takes the wait loop out of `entrypoint.sh`. Nobody has tested
that. `entrypoint.sh` and `config/50-audio-operator.conf` both still
name device reservation as a reason for the bus, so a test that
confirms this finding also corrects those two comments.
