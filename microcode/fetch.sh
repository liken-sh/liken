#!/usr/bin/env bash
#
# Vendor the CPU microcode and pack it as the early cpio.
#
# Microcode is load-bearing for security, not optional. Spectre-class
# mitigations degrade silently on stale microcode, and Intel
# increasingly forbids loading updates late in boot. So the kernel
# loads microcode before almost anything else, and the loading
# convention is its own: an uncompressed cpio that holds
# kernel/x86/microcode/GenuineIntel.bin and AuthenticAMD.bin, placed
# ahead of the real initrd. The kernel scans the very start of its
# initrd for exactly these member names before it decompresses
# anything, which is why this cpio must stay uncompressed and must
# come first. Boot entries name it as their first initrd; the lab's
# -kernel boot concatenates it in front, which is the same file
# layout either way.
#
# Each vendor's .bin is a concatenation of per-family update blobs,
# and the kernel scans it for the entry that matches the running CPU.
# Concatenation is the format, so this script assembles the files
# with cat and needs no microcode-specific tool.
#
# The two vendors come from two upstreams:
#
#   Intel publishes microcode in its own repository, pinned here by
#   release tag (microcode/VERSION) and digest, fetched as the tag's
#   source tarball. Both vendors ship unconditionally: Intel's blob
#   is about 21 MiB and AMD's about 1 MiB, and nothing that small
#   needs a decision either way.
#
#   AMD publishes microcode through linux-firmware, so the AMD half
#   comes from that domain's already-fetched, already-verified tree
#   (the same reuse as the licensing domain's place()). The AMD
#   files' provenance pin is linux-firmware/VERSION, not this
#   domain's.
#
# On licensing: both vendors' terms allow redistribution of the
# binary and forbid modification, and no source exists to mirror.
# The licensing domain carries their notices (licensing/NOTICES.md);
# this is the one component category with notices and no source
# mirror.
#
# Usage:
#   microcode/fetch.sh    fetch and assemble the pinned version
#
# The result lands in microcode/dist/<version>/microcode.cpio,
# cached in microcode/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl sha256sum tar cpio; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

version="$(cat "$here/VERSION")"
tarball="intel-microcode-$version.tar.gz"
url="https://github.com/intel/Intel-Linux-Processor-Microcode-Data-Files/archive/refs/tags/microcode-$version.tar.gz"

digest="5a07ce745d0bd8b360a4713564d46d5e38be797316a52abedaff0761e1b02370"

linuxfirmware_version="$(cat "$here/../linux-firmware/VERSION")"
amd="$here/../linux-firmware/cache/$linuxfirmware_version/amd-ucode"
# The AMD families live in linux-firmware's cache, which CI does not
# keep between runs. The domain's own fetch is idempotent and
# verifying, so re-running it here is always safe.
[[ -d "$amd" ]] || "$here/../linux-firmware/fetch.sh"

cache="$here/cache"
mkdir -p "$cache"

if ! sha256sum --check --status <<<"$digest  $cache/$tarball" >/dev/null 2>&1; then
    echo "downloading intel microcode $version"
    curl -fL --progress-bar -o "$cache/$tarball" "$url"
    sha256sum --check --quiet <<<"$digest  $cache/$tarball"
fi

out="$here/dist/$version"
rm -rf "$out"

# The staging tree is the cpio's exact member layout.
staged="$out/staged"
mkdir -p "$staged/kernel/x86/microcode"

# Intel: every per-family file under intel-ucode/, concatenated. The
# tarball also carries intel-ucode-with-caveats/ (updates that need
# special OS handling); those stay out, the same choice every
# distribution makes.
tar -xzf "$cache/$tarball" -C "$out" --strip-components=1 \
    --wildcards "*/intel-ucode" "*/license"
cat "$out/intel-ucode"/* >"$staged/kernel/x86/microcode/GenuineIntel.bin"

# AMD: every family's container file, concatenated. The .asc
# signatures stay behind; the kernel checks microcode by its own
# means, not by PGP.
cat "$amd"/microcode_amd*.bin >"$staged/kernel/x86/microcode/AuthenticAMD.bin"

# The vendors' license texts travel in the dist, named the way the
# licensing domain's appendix headings read. The LICENSES.md build
# references them from here, so the texts always match the fetched
# pin; they are not part of the cpio.
cp "$out/license" "$out/Intel-microcode.txt"
cp "$here/../linux-firmware/cache/$linuxfirmware_version/LICENSES/LICENSE.amd-ucode" \
    "$out/AMD-microcode.txt"

# The early cpio: newc format, owned by root, deliberately not
# compressed (the kernel's early scanner reads it raw).
(cd "$staged" && find . -type f | cpio --quiet -o -H newc -R +0:+0) \
    >"$out/microcode.cpio"
rm -rf "$staged" "$out/intel-ucode" "$out/license"

echo "microcode $version:"
du -sh "$out/microcode.cpio" | cut -f1 | xargs -I{} echo "  {} (GenuineIntel from intel $version, AuthenticAMD from linux-firmware $linuxfirmware_version)"
