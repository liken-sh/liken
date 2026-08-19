#!/bin/sh
# Collects PipeWire, WirePlumber, and every file the two daemons open
# at runtime into one directory tree, so the image ships that tree and
# nothing else. Run it in a builder that has the Debian packages
# installed, with the output directory as the argument. The sibling
# display operator's weston-closure.sh is the same shape for the same
# reason.
set -eu

out=$1

# The multiarch directory that holds every library below, read from
# dpkg so that no architecture is written down here.
lib=$(dirname "$(dpkg -L libpipewire-0.3-modules | grep '/pipewire-0.3$')")

# ldd reports the DT_NEEDED graph and nothing about a file a program
# opens by name at runtime. Every module below is such a file. Each
# one is named because the running daemons mapped it, and the module
# directories hold many more that this operator never loads. The
# dependency walk of those modules is where the image size would come
# back.
#
# pipewire loads the eleven modules its packaged pipewire.conf names,
# minus the four that have the ifexists flag (rt, portal, x11-bell,
# jackdbus-detect), which it skips when the file is absent.
#
# wireplumber's main-embedded profile loads these nine, and the
# operator's own configuration turns off the monitors whose modules
# are not here.
#
# The bluez monitor is a Lua script under /usr/share/wireplumber,
# not a module, and the pipewire modules it uses are the adapter,
# client-device, and client-node already named below, so enabling
# it adds no wireplumber module to this list.
#
# The SPA plugins are the media layer: audioconvert and audiomixer
# for every adapter node, support for the loop and the logger, dbus
# for the client libraries that ask for a bus.
#
# libspa-alsa.so opens no card on a machine that has none, so no
# runtime check reports it loading. It is named here because a machine
# with speakers is the machine this image is for, and the release gate
# asserts it by name for the same reason.
#
# pw-dump is how the operator reads the graph, and wpctl is the
# wireplumber container's probe. kubectl exec runs both by name,
# which is the only way to run anything in an image with no shell.
#
# The bluez5 plugin loads by name when the declare container
# enables WirePlumber's Bluetooth monitor. The codec plugins beside
# it are dlopened one at a time as bluez5/libspa-codec-bluez5-
# <name>.so, so each one has to be named here to be offered. Every
# one of them ships in libspa-0.2-bluetooth; none is an extra pack.
seeds="
/usr/bin/pipewire
/usr/bin/wireplumber
/usr/bin/pw-dump
/usr/bin/wpctl
$lib/pipewire-0.3/libpipewire-module-access.so
$lib/pipewire-0.3/libpipewire-module-adapter.so
$lib/pipewire-0.3/libpipewire-module-client-device.so
$lib/pipewire-0.3/libpipewire-module-client-node.so
$lib/pipewire-0.3/libpipewire-module-link-factory.so
$lib/pipewire-0.3/libpipewire-module-metadata.so
$lib/pipewire-0.3/libpipewire-module-profiler.so
$lib/pipewire-0.3/libpipewire-module-protocol-native.so
$lib/pipewire-0.3/libpipewire-module-session-manager.so
$lib/pipewire-0.3/libpipewire-module-spa-device-factory.so
$lib/pipewire-0.3/libpipewire-module-spa-node-factory.so
$lib/spa-0.2/alsa/libspa-alsa.so
$lib/spa-0.2/audioconvert/libspa-audioconvert.so
$lib/spa-0.2/audiomixer/libspa-audiomixer.so
$lib/spa-0.2/support/libspa-dbus.so
$lib/spa-0.2/support/libspa-support.so
$lib/spa-0.2/bluez5/libspa-bluez5.so
$lib/spa-0.2/bluez5/libspa-codec-bluez5-aptx.so
$lib/spa-0.2/bluez5/libspa-codec-bluez5-faststream.so
$lib/spa-0.2/bluez5/libspa-codec-bluez5-g722.so
$lib/spa-0.2/bluez5/libspa-codec-bluez5-lc3.so
$lib/spa-0.2/bluez5/libspa-codec-bluez5-ldac.so
$lib/spa-0.2/bluez5/libspa-codec-bluez5-opus-g.so
$lib/spa-0.2/bluez5/libspa-codec-bluez5-opus.so
$lib/spa-0.2/bluez5/libspa-codec-bluez5-sbc.so
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

# The data trees, every one read by name at startup, so nothing in
# the library graph points at them: pipewire reads its own
# pipewire.conf and client.conf from /usr/share/pipewire, wireplumber
# reads wireplumber.conf and every Lua script under
# /usr/share/wireplumber, and the ALSA plugin reads the card and PCM
# definitions under /usr/share/alsa.
#
# The three gconv index files are what glibc maps on the first
# iconv_open. The charset modules beside them stay behind, because
# the conversions this pod runs are between UTF-8 and glibc's
# internal form, and both are built into libc.
#
# /usr/share/spa-0.2 holds bluez5/bluez-hardware.conf, the quirks
# database the bluez5 plugin opens by that path at startup. It maps
# known adapters and speakers to the features each one may use.
data="
/usr/share/pipewire
/usr/share/wireplumber
/usr/share/alsa
/usr/share/spa-0.2
$lib/gconv/gconv-modules
$lib/gconv/gconv-modules.cache
$lib/gconv/gconv-modules.d
"

# Prints every hop of a symlink chain and then the file at the end,
# because a soname is a link to a versioned file and the loader opens
# the soname.
hops() {
	path=$1
	while [ -L "$path" ]; do
		printf '%s\n' "$path"
		target=$(readlink "$path")
		case $target in
		/*) path=$target ;;
		*) path=$(dirname "$path")/$target ;;
		esac
	done
	printf '%s\n' "$path"
}

# ldd prints the whole DT_NEEDED graph of one file, so one call per
# seed reaches every library the loader resolves at load time.
# linux-vdso has no file behind it, and the loader's own line prints
# with no arrow, which is what the two sed patterns accept.
needed() {
	ldd "$1" | sed -n 's/.*=> \(\/[^ ]*\).*/\1/p; s/^\t\(\/[^ ]*\) (0x.*/\1/p'
}

# /lib and /lib64 are symlinks into /usr. ldd reports every library
# under the name it resolved through them, and the loader's own path
# names /lib64. The two links go in first, so every copy below writes
# the path exactly as it was resolved.
mkdir -p "$out$lib" "$out/usr/lib64" "$out/usr/bin"
for link in /lib /lib64; do
	if [ -L "$link" ]; then
		cp -a --parents "$link" "$out"
	fi
done

for seed in $seeds; do
	{
		hops "$seed"
		needed "$(readlink -f "$seed")" | while read -r path; do hops "$path"; done
	} >>"$out/.closure"
done
sort -u "$out/.closure" | while read -r path; do
	cp -a --parents "$path" "$out"
done
rm -f "$out/.closure"

for path in $data; do
	cp -a --parents "$path" "$out"
done

# Without a cache the loader searches its built-in directory list on
# every open, and that list does not name the multiarch directory
# that holds every library above.
mkdir -p "$out/etc"
printf '%s\n' "$lib" >"$out/etc/ld.so.conf"
ldconfig -r "$out"
