#!/bin/sh
# Builds liken-layout.so and liken-arrange-drill from the sources beside
# this script, into the directory named by the first argument. The
# weston Dockerfile and the smoke tests both run it, so the module that
# a smoke test loads is built the same way as the one that ships. Any
# further arguments are extra compiler flags, such as
# -fsanitize=address for a smoke run under AddressSanitizer.
#
# wayland-scanner writes the code of liken_arrangement_v1 from its XML at
# build time, so the protocol has one source.
#
# The module links nothing but libm: weston resolves every libweston
# and libwayland symbol in it at dlopen, which is why pkg-config gives
# only the include paths. Every symbol but wet_module_init is hidden,
# so no name in the module can stand in for one in weston.
#
# The drill links libwayland-client statically when the static archive
# is installed, so it runs in any debug container on the same libc. A
# sanitizer build links it dynamically, because the sanitizers' runtimes
# do not link statically.
set -eu

src=$(cd "$(dirname "$0")" && pwd)
out=$1
shift
gen=$(mktemp -d)
trap 'rm -rf "$gen"' EXIT

protocols=$(pkg-config --variable=pkgdatadir wayland-protocols)
xdg_shell=$protocols/stable/xdg-shell/xdg-shell.xml

wayland-scanner server-header "$src/liken-arrangement-v1.xml" "$gen/liken-arrangement-v1-server-protocol.h"
wayland-scanner client-header "$src/liken-arrangement-v1.xml" "$gen/liken-arrangement-v1-client-protocol.h"
wayland-scanner private-code "$src/liken-arrangement-v1.xml" "$gen/liken-arrangement-v1-protocol.c"
wayland-scanner client-header "$xdg_shell" "$gen/xdg-shell-client-protocol.h"
wayland-scanner private-code "$xdg_shell" "$gen/xdg-shell-protocol.c"
for xml in stable/viewporter/viewporter.xml staging/single-pixel-buffer/single-pixel-buffer-v1.xml; do
	name=$(basename "$xml" .xml)
	wayland-scanner client-header "$protocols/$xml" "$gen/$name-client-protocol.h"
	wayland-scanner private-code "$protocols/$xml" "$gen/$name-protocol.c"
done

mkdir -p "$out"

# shellcheck disable=SC2046  # pkg-config prints several flags.
gcc -Wall -Wextra -Werror -O2 -shared -fPIC -fvisibility=hidden "$@" \
	-I"$gen" $(pkg-config --cflags libweston-16 wayland-server pixman-1) \
	-o "$out/liken-layout.so" \
	"$src/liken-layout.c" "$src/motion.c" "$src/arrangement.c" \
	"$gen/liken-arrangement-v1-protocol.c" -lm

static=
case "$*" in
*sanitize*) ;;
*) if [ -e "$(pkg-config --variable=libdir wayland-client)/libwayland-client.a" ]; then
	static=-static
fi ;;
esac
# shellcheck disable=SC2046  # pkg-config prints several flags.
gcc -Wall -Wextra -Werror -O2 $static "$@" \
	-I"$gen" $(pkg-config --cflags wayland-client) \
	-o "$out/liken-arrange-drill" \
	"$src/liken-arrange-drill.c" "$gen/liken-arrangement-v1-protocol.c" \
	"$gen/xdg-shell-protocol.c" "$gen/viewporter-protocol.c" \
	"$gen/single-pixel-buffer-v1-protocol.c" \
	$(pkg-config ${static:+--static} --libs wayland-client)
