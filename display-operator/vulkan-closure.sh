#!/bin/sh
# Collects the Vulkan loader, the two drivers liken's machines draw
# with, and the client libraries a Wayland program opens by name, into
# one directory tree. The tree is the ghcr.io/liken-sh/vulkan image, the
# base that every Vulkan client liken ships builds on.
#
# Debian puts every Vulkan driver in one package, and two of the seven,
# lavapipe and the AMD driver, link LLVM. Lavapipe draws on the CPU, and
# no liken machine runs it: every screen is a cable into a GPU. So the
# tree keeps the Intel and AMD drivers and leaves the other five, and
# LLVM comes along for AMD alone.
#
# Run it in a builder that has the packages installed. It writes a
# rootfs to the directory named on the command line.
set -eu

. "$(dirname "$0")/closure.sh"

out=$1

# Every seed here is a load that ldd cannot report from the program,
# because a Vulkan client links none of them. The toolkit opens
# libvulkan.so.1, libwayland-client.so.0 and libxkbcommon.so.0 by name
# at startup. The loader reads the ICD files below and opens each driver
# they name.
seeds="
$lib/libvulkan.so.1
$lib/libvulkan_intel.so
$lib/libvulkan_radeon.so
$lib/libwayland-client.so.0
$lib/libxkbcommon.so.0
"

# The data files, each read by name at runtime.
#
# The loader finds a driver only through its ICD file. Mesa reads drirc
# for the per-application workarounds it applies. xkbcommon compiles a
# keymap from the rules, the symbols and the keycodes under
# /usr/share/X11/xkb whenever a keyboard arrives. zoneinfo is the time
# zone database: Rust's standard library has no time zones, and a
# client that shows a clock reads the zone TZ names from here.
data="
/usr/share/vulkan/icd.d/intel_icd.json
/usr/share/vulkan/icd.d/radeon_icd.json
/usr/share/drirc.d/00-mesa-defaults.conf
/usr/share/X11/xkb
/usr/share/zoneinfo
"

collect "$out" "$seeds" "$data"
