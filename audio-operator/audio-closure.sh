#!/bin/sh
# Copies PipeWire, WirePlumber, the tools that the pod runs, and every
# file that they open at runtime into one directory tree, which is all
# the image ships. Run it in a builder that has the Debian packages
# installed, with the output directory as the argument.
#
# collect, notices, and $lib, the multiarch library directory, come
# from vulkan/closure.sh, which the Dockerfile copies in from the build
# context named builder.
set -eu

# shellcheck source=../vulkan/closure.sh
. "$(dirname "$0")/closure.sh"

out=$1

# coreutils installs libstdbuf.so in a private directory that differs
# between releases, so the path comes from dpkg.
stdbuf=$(dpkg -L coreutils | grep '/libstdbuf\.so$')

# collect copies each seed and the libraries that ldd reports for it.
# ldd cannot report a file that a program opens by name at runtime, and
# every module below is such a file, so each is listed. The module
# directories hold many more modules that this operator never loads,
# and copying them with their libraries would make the image larger.
#
# pipewire loads the modules named in its packaged pipewire.conf,
# except the ones marked ifexists (portal, x11-bell, jackdbus-detect),
# which it skips when the file is absent. rt is marked ifexists too,
# and it is listed, because without it PipeWire's data thread runs at
# normal priority and a bitstream drops out at each underrun
# (50-pipewire-realtime.conf).
#
# wireplumber loads the modules that its main-embedded profile needs.
# The operator's configuration disables the monitors whose modules are
# not here. The bluez monitor is a Lua script under /usr/share/wireplumber,
# and the PipeWire modules it uses (adapter, client-device, client-node)
# are already listed, so enabling it needs no other module.
#
# The SPA plugins are the media layer. audioconvert and audiomixer
# serve every adapter node, support provides the loop and logger, and
# dbus serves client libraries that ask for a bus. libspa-support.so
# also provides the support.null-audio-sink factory, which the smoke
# test (smoke/audio-operator.sh) uses to create a sink, because the CI
# runner has no sound card and the test's capture stream needs a sink
# to link to.
#
# libspa-alsa.so opens no card on a machine without one, so the daemons
# do not map it in CI. It is listed because this image is for machines
# with sound cards, and the smoke test checks for it by name.
#
# The operator runs pw-dump to read the graph, and the smoke test runs
# wpctl against WirePlumber.
#
# A person runs pw-top and pw-metadata with kubectl exec. pw-top -b -n 1
# prints one reading of every node with its xrun count, which counts
# dropouts. Without it, the only reading is a debug environment
# variable that takes two pod restarts to set. pw-metadata reads and
# writes the settings in the graph's metadata object, for example
# clock.force-quantum. The operator also runs pw-cli set-param: a
# prepare request that sets a codec writes it to the bluez5 device, and
# every prepare of a speaker sets the delivered sink's level to unity.
# pw-top needs libncursesw and libtinfo, pw-cli needs libreadline, and
# pw-metadata needs no other libraries.
#
# The capture container runs pw-record to tap a sink's monitor ports or
# a source's output ports, and pipes its raw samples into flac or
# opusenc. pw-record is the pw-cat binary under another name, so both
# the link and its target are listed. The Go process does not encode
# because no pure-Go Opus encoder exists. The container preloads
# libstdbuf.so into flac and opusenc with _STDBUF_O=0, because both
# write to stdout through stdio, and without it a silent sink would
# deliver nothing until the tap ends.
#
# pw-cat links libsndfile, which brings libvorbisenc, libvorbis,
# libmpg123, and libmp3lame. The capture container runs pw-record with
# --raw, so it never opens those codec libraries, but they are copied
# with the dependency. libopus is already here for
# libspa-codec-bluez5-opus.so, so opusenc adds only the encoder.
#
# libspa-bluez5.so is loaded by name when the declare container
# enables WirePlumber's Bluetooth monitor. It loads each codec plugin
# beside it as bluez5/libspa-codec-bluez5-<name>.so, so the pattern
# below takes every codec plugin that libspa-0.2-bluetooth installs,
# including one that a newer PipeWire adds. The bluez5 plugin refuses
# to start when a codec it requires is missing, such as the HFP CVSD
# codec, and WirePlumber then registers no endpoint with bluetoothd.
seeds="
/usr/bin/pipewire
/usr/bin/wireplumber
/usr/bin/pw-dump
/usr/bin/wpctl
/usr/bin/pw-top
/usr/bin/pw-cli
/usr/bin/pw-metadata
/usr/bin/pw-cat
/usr/bin/pw-record
/usr/bin/flac
/usr/bin/opusenc
$stdbuf
$lib/pipewire-0.3/libpipewire-module-access.so
$lib/pipewire-0.3/libpipewire-module-adapter.so
$lib/pipewire-0.3/libpipewire-module-client-device.so
$lib/pipewire-0.3/libpipewire-module-client-node.so
$lib/pipewire-0.3/libpipewire-module-link-factory.so
$lib/pipewire-0.3/libpipewire-module-metadata.so
$lib/pipewire-0.3/libpipewire-module-profiler.so
$lib/pipewire-0.3/libpipewire-module-protocol-native.so
$lib/pipewire-0.3/libpipewire-module-rt.so
$lib/pipewire-0.3/libpipewire-module-session-manager.so
$lib/pipewire-0.3/libpipewire-module-spa-device-factory.so
$lib/pipewire-0.3/libpipewire-module-spa-node-factory.so
$lib/spa-0.2/alsa/libspa-alsa.so
$lib/spa-0.2/audioconvert/libspa-audioconvert.so
$lib/spa-0.2/audiomixer/libspa-audiomixer.so
$lib/spa-0.2/support/libspa-dbus.so
$lib/spa-0.2/support/libspa-support.so
$lib/spa-0.2/bluez5/libspa-bluez5.so
$lib/spa-0.2/bluez5/libspa-codec-bluez5-*.so
$lib/wireplumber-0.5/libwireplumber-module-default-nodes-api.so
$lib/wireplumber-0.5/libwireplumber-module-log-settings.so
$lib/wireplumber-0.5/libwireplumber-module-lua-scripting.so
$lib/wireplumber-0.5/libwireplumber-module-mixer-api.so
$lib/wireplumber-0.5/libwireplumber-module-settings.so
$lib/wireplumber-0.5/libwireplumber-module-si-audio-adapter.so
$lib/wireplumber-0.5/libwireplumber-module-si-node.so
$lib/wireplumber-0.5/libwireplumber-module-si-standard-link.so
$lib/wireplumber-0.5/libwireplumber-module-standard-event-source.so
"

# pipewire, wireplumber, and the ALSA plugin read these trees by name
# at startup, so no library graph points at them: pipewire.conf and
# client.conf under /usr/share/pipewire, wireplumber.conf and every
# Lua script under /usr/share/wireplumber, and the card and PCM
# definitions under /usr/share/alsa.
#
# glibc maps the gconv index files on the first iconv_open. The
# charset modules beside them are left out, because this pod converts
# only between UTF-8 and glibc's internal form, which libc has built
# in.
#
# The bluez5 plugin opens /usr/share/spa-0.2/bluez5/bluez-hardware.conf
# at startup. It maps known adapters and speakers to the features each
# may use.
data="
/usr/share/pipewire
/usr/share/wireplumber
/usr/share/alsa
/usr/share/spa-0.2
$lib/gconv/gconv-modules
$lib/gconv/gconv-modules.cache
$lib/gconv/gconv-modules.d
"

collect "$out" "$seeds" "$data"
notices "$out" audio-operator
