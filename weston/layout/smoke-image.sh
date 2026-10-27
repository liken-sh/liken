#!/usr/bin/env bash
# Builds the image that the layout smoke tests run: the build stage of
# weston/Dockerfile, which holds the weston of the release built from
# its tarball on the same Debian image and snapshot date, with the shm
# client and weston-screenshooter, and then liken-layout.so and
# liken-arrange-drill from build.sh, as in the release.
#
# Usage: smoke-image.sh <image tag> [extra compiler flags]
#
# With -fsanitize=address among the flags, the image also holds
# libasan, and LIKEN_ASAN in the image's environment names it, so a
# test can preload it into weston: weston itself is not built with the
# sanitizer, and a module built with it needs its runtime loaded first.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
image=$1
shift
cflags="$*"

base=$(sed -n 's/^debian = "\(.*\)"$/\1/p' "$here/../../toolchains/images.toml")
snapshot=$(sed -n 's/^version = "\([0-9]*\)"$/\1/p' "$here/../package.toml")
: "${base:?toolchains/images.toml names no debian image}"
: "${snapshot:?weston/package.toml names no snapshot date}"

echo "== building weston on $base at snapshot $snapshot"
# The build stage of weston/Dockerfile, with the same weston, the same
# libweston headers, and the same libraries as the release.
docker buildx build -q --load -t "$image-build" --target build \
	--build-arg VERSION="$snapshot" \
	--build-context debian="docker-image://$base" \
	--build-context builder="$here/../../vulkan" \
	-f "$here/../Dockerfile" "$here/.." >/dev/null

echo "== building $image${cflags:+ with $cflags}"
docker build -q -t "$image" -f - \
	--build-arg BASE="$image-build" --build-arg CFLAGS="$cflags" \
	"$here" >/dev/null <<'DOCKERFILE'
ARG BASE
FROM $BASE
COPY . /build/
ARG CFLAGS
# shellcheck disable=SC2086  # CFLAGS holds several flags or none.
RUN sh /build/build.sh /build/out $CFLAGS \
    && cp /build/out/liken-layout.so /usr/lib/x86_64-linux-gnu/weston/ \
    && cp /build/out/liken-arrange-drill /usr/local/bin/ \
    && echo "LIKEN_ASAN=$(gcc -print-file-name=libasan.so)" >/etc/liken-asan
DOCKERFILE
