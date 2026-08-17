#!/bin/sh
# Collects weston and everything it loads into one directory tree, so
# that the image ships that tree and nothing else.
#
# Debian puts every libweston backend in one package, so apt cannot
# express what this image needs. Installing weston pulls FreeRDP,
# neatvnc, GStreamer, PipeWire, libavcodec and flite, because
# rdp-backend.so, vnc-backend.so and remoting-plugin.so sit in the same
# package as drm-backend.so. This script takes the four modules the
# operator uses and leaves the other eight behind.
#
# Run it in a builder that has the packages installed. It writes a
# rootfs to the directory named on the command line.
set -eu

out=$1

# The multiarch directory that every library below lives in. dpkg
# names it for the architecture this builds on, so no architecture is
# written down here.
lib=$(dirname "$(dpkg -L libweston-14-0 | grep '/libweston-14$')")

# The dynamic loader finds a library by its DT_NEEDED name, and ldd
# reports that whole graph. It reports nothing about a library that a
# program opens by file name at runtime, and every line below is such a
# library. Each one is a load that ldd cannot see:
#
#   weston            opens the backend, the renderer and the shell
#                     that weston.ini names.
#   libEGL.so.1       is glvnd's dispatch. It reads the vendor's JSON
#                     under /usr/share/glvnd and opens the library the
#                     JSON names, which for mesa is libEGL_mesa.so.0.
#   libgbm.so.1       opens a backend from the gbm directory.
#                     dri_gbm.so is mesa's.
#   libEGL_mesa.so.0  opens the DRI driver for the card, by the name
#                     the kernel driver reports: iris_dri.so on Intel,
#                     radeonsi_dri.so on AMD.
#
# The DRI names are all symlinks to one libdril_dri.so, so the whole
# set costs one file plus fourteen links, and the image runs on any
# card mesa supports rather than on the card this was built for.
#
# wayland-info is the one program here that the compositor does not
# open. It is the diagnostic: it lists every global the compositor
# advertises, and kubectl exec runs it by name in an image that has no
# shell to run it from.
seeds="
/usr/bin/weston
/usr/bin/wayland-info
$lib/libweston-14/drm-backend.so
$lib/libweston-14/headless-backend.so
$lib/libweston-14/gl-renderer.so
$lib/weston/kiosk-shell.so
$lib/libEGL_mesa.so.0
$lib/gbm/dri_gbm.so
$lib/dri/libdril_dri.so
$lib/dri/iris_dri.so
$lib/dri/crocus_dri.so
$lib/dri/i915_dri.so
$lib/dri/radeonsi_dri.so
$lib/dri/r300_dri.so
$lib/dri/r600_dri.so
$lib/dri/nouveau_dri.so
$lib/dri/virtio_gpu_dri.so
$lib/dri/vmwgfx_dri.so
$lib/dri/d3d12_dri.so
$lib/dri/zink_dri.so
$lib/dri/swrast_dri.so
$lib/dri/kms_swrast_dri.so
"

# The data files. Every one is read by name at runtime, so nothing in
# the library graph points at them either.
#
# glvnd refuses to load a vendor it has no JSON for. mesa reads
# drirc for the per-application workarounds it applies. xkbcommon
# compiles a keymap for the seat whenever weston starts, and it
# compiles that keymap from the rules, the symbols and the keycodes
# under /usr/share/X11/xkb, with require-input=false and no keyboard
# on the machine. libinput reads its quirks database whether or not a
# device is there to apply one to, and it says "failed to find data
# files" at every start without it.
data="
/usr/share/glvnd/egl_vendor.d/50_mesa.json
/usr/share/drirc.d/00-mesa-defaults.conf
/usr/share/X11/xkb
/usr/share/libinput
"

# Every hop of a symlink chain, then the file at the end of it. A
# soname is a link to a versioned file, and the loader opens the
# soname, so copying only one end of the chain breaks the load.
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

# ldd prints the whole DT_NEEDED graph of one file, so one call for
# each seed reaches every library the loader resolves at load time.
# linux-vdso has no file behind it, and the loader's own line prints
# with no arrow.
needed() {
	ldd "$1" | sed -n 's/.*=> \(\/[^ ]*\).*/\1/p; s/^\t\(\/[^ ]*\) (0x.*/\1/p'
}

# /lib and /lib64 are symlinks to the directories under /usr, and ldd
# reports every library under the name it resolved, which is the one
# that goes through them. The loader's own path names /lib64. Copying
# the two links first lets every copy below write the path exactly as
# it was resolved.
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
# every library above lives in.
mkdir -p "$out/etc"
printf '%s\n' "$lib" >"$out/etc/ld.so.conf"
ldconfig -r "$out"
