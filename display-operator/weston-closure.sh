#!/bin/sh
# Collects weston, everything it loads, and the two diagnostic
# programs into one directory tree, so that the image ships that tree
# and nothing else.
#
# Debian puts every libweston backend in one package, so apt cannot
# express what this image needs. Installing weston pulls FreeRDP,
# neatvnc, GStreamer, PipeWire, libavcodec and flite, because
# rdp-backend.so, vnc-backend.so and remoting-plugin.so are in the same
# package as drm-backend.so. This script takes the four modules the
# operator uses and omits the other eight.
#
# Run it in a builder that has the packages installed. It writes a
# rootfs to the first directory named on the command line, less every
# file the second directory already holds. The second is the tree of
# the vulkan image, which the weston image builds FROM, so the two
# share glibc, libdrm, libwayland and the LLVM that gallium and the AMD
# Vulkan driver both link.
set -eu

. "$(dirname "$0")/closure.sh"

out=$1
base=$2

# The dynamic loader finds a library by its DT_NEEDED name, and ldd
# reports that whole graph. It reports nothing about a library that a
# program opens by file name at runtime, and every line below is such a
# library. Each one is a load that ldd cannot report:
#
#   weston            opens the backend, the renderer, the shell and
#                     the modules that weston.ini names. The one
#                     module here, liken-layout.so, is not a seed: the
#                     Dockerfile builds it and copies it into the
#                     weston stage, the way it copies the hotplug shim.
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
# set costs one file plus thirteen links, and the image runs on any
# card mesa supports rather than on the card this was built for.
#
# Two programs here are ones the compositor does not open. They are
# the diagnostics, and kubectl exec runs each by name in an image that
# has no shell to run them from. wayland-info answers for the
# compositor: it lists every global the compositor advertises, and it
# is the first thing to read when a client connects and draws nothing.
# ddcutil answers for the panels: it reads a panel's whole
# capabilities string and every VCP code, where the operator asks for
# two, so it says whether the panel or the operator is the part that
# is wrong when a monitor publishes no control attribute. A consumer
# pod that holds a control device runs its own copy; this one is for
# the person reading the operator's own node.
seeds="
/usr/bin/weston
/usr/bin/wayland-info
/usr/bin/ddcutil
$lib/libweston-14/drm-backend.so
$lib/libweston-14/headless-backend.so
$lib/libweston-14/gl-renderer.so
$lib/weston/ivi-shell.so
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
# compiles a keymap for the seat whenever weston starts, even with
# require-input=false and no keyboard on the machine. It compiles
# that keymap from the rules, the symbols and the keycodes under
# /usr/share/X11/xkb. libinput reads its quirks database whether or not a
# device is there to apply one to, and it says "failed to find data
# files" at every start without it.
data="
/usr/share/glvnd/egl_vendor.d/50_mesa.json
/usr/share/drirc.d/00-mesa-defaults.conf
/usr/share/X11/xkb
/usr/share/libinput
"

collect "$out" "$seeds" "$data"
subtract "$out" "$base"
