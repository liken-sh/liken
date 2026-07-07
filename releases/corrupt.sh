#!/usr/bin/env bash
#
# Deliberately damage a published release, for drilling the trust
# chain: flip one byte a megabyte into liken.cpio, leaving
# release.yaml promising a digest the bytes no longer match. A machine
# asked to run this release must refuse to stage it — the download
# completes, the verification fails, and the VersionConverged
# condition holds at DigestMismatch with nothing written to a slot.
#
# The byte is *complemented*, not overwritten with a constant, so the
# damage is guaranteed whatever value was there. dd's conv=notrunc
# patches in place instead of truncating the file at the seek point.
set -euo pipefail

version="$1"
here="$(cd "$(dirname "$0")" && pwd)"
target="$here/dist/$version/liken.cpio"
offset=$((1024 * 1024))

byte="$(od -An -tu1 -j"$offset" -N1 "$target" | tr -d ' ')"
flipped=$((255 - byte))
printf '%b' "\\$(printf '%03o' "$flipped")" |
    dd of="$target" bs=1 seek="$offset" conv=notrunc status=none

echo "flipped the byte at offset $offset of $target ($byte -> $flipped)"
echo "release.yaml still promises the original digest; a machine must now refuse this release"
