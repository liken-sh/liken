#!/bin/sh
# Collects mpv and everything it opens by name into one directory tree,
# less every file the trees named after it hold. The tree is the
# ghcr.io/liken-sh/mpv image, the base the media operator's player
# builds on.
#
# Run it in a builder that has the packages installed. It writes a
# rootfs to the first directory named on the command line, less every
# file the directories after it already hold.
set -eu

. "$(dirname "$0")/closure.sh"

out=$1
shift

# mpv is the only program. Every line under it is a file mpv's own
# process opened by name in a traced playback run, so no DT_NEEDED entry
# reaches it. libpipewire loads the seven modules client.conf names and
# the SPA plugins its context.spa-libs maps; libspa-support carries the
# loop and the logger, and libspa-dbus is what it opens for a client
# that asks for a bus. The audiomixer, control, and videoconvert plugins
# are named in the configuration and loaded by no traced run; they are
# tens of kilobytes each and are here so a path the trace missed does
# not fail at the first play. libpulse opens libpulsecommon at process
# start, whether or not --ao=pulse is selected, and its name carries
# Debian's soname version the way weston-closure.sh names libweston 14:
# a suite that bumps it fails this build, which is the report that the
# list needs reading again.
seeds="
/usr/bin/mpv
$lib/pipewire-0.3/libpipewire-module-adapter.so
$lib/pipewire-0.3/libpipewire-module-client-device.so
$lib/pipewire-0.3/libpipewire-module-client-node.so
$lib/pipewire-0.3/libpipewire-module-metadata.so
$lib/pipewire-0.3/libpipewire-module-protocol-native.so
$lib/pipewire-0.3/libpipewire-module-rt.so
$lib/pipewire-0.3/libpipewire-module-session-manager.so
$lib/spa-0.2/audioconvert/libspa-audioconvert.so
$lib/spa-0.2/audiomixer/libspa-audiomixer.so
$lib/spa-0.2/control/libspa-control.so
$lib/spa-0.2/videoconvert/libspa-videoconvert.so
$lib/spa-0.2/support/libspa-dbus.so
$lib/spa-0.2/support/libspa-support.so
$lib/pulseaudio/libpulsecommon-17.0.so
"

# libpipewire refuses to build a client context without client.conf.
# libass resolves the OSD font family through fontconfig, which reads
# fonts.conf and every file the links under /etc/fonts/conf.d point at,
# half of them under /usr/share/fontconfig. /etc/fonts copies whole, so
# it also carries the configuration of a font family the builder
# installed and the image does not, which is harmless.
data="
/usr/share/pipewire/client.conf
/etc/fonts
/usr/share/fontconfig
"

collect "$out" "$seeds" "$data"

# The image is FROM ffmpeg, which is FROM vaapi, which is FROM vulkan, so
# the layer carries none of the three trees.
for base in "$@"; do
	subtract "$out" "$base"
done
