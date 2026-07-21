#!/usr/bin/env bash
#
# Derive the firmware set this OS ships. The set is not curated by
# anyone's judgment: every kernel module declares the firmware it may
# request (MODULE_FIRMWARE, readable with modinfo), so the set to
# ship is the union of those declarations over the kernel build's
# whole module tree. The kernel pin defines the set, and a kernel
# bump re-derives it.
#
# The full linux-firmware tree is about 1.9 GB, and most of it
# describes hardware that an x86 server kernel cannot drive (ARM
# SoCs, phone parts). Derivation keeps what this kernel can actually
# ask for. Resolution has three forms, in order:
#
#   1. A literal name that exists in the tree ships as that file.
#   2. A name with glob characters (some drivers declare a whole
#      directory, for example "ath11k/QCA6390/hw2.0/*") ships every
#      match. Modern Qualcomm Wi-Fi ships only through this form.
#   3. A name that matches a WHENCE "Link:" alias ships its target,
#      and the alias itself ships as a symlink. Drivers request the
#      alias name, so the link is load-bearing, not decoration.
#
# One exception is named, not derived: the nvidia/ directory. Those
# GSP blobs serve display and compute paths that a headless OS does
# not use, and they are large. liken has no GPU-compute design yet;
# when it grows one, that milestone re-decides this. The composable
# release design is the option for anyone who needs them sooner: an
# nvidia-inclusive image is a rebuild with one more directory.
#
# A name that resolves no way at all is recorded in the manifest.
# These are drivers whose firmware upstream never shipped (some
# vendors forbid redistribution) or names a driver constructs at
# runtime. Derivation gives a floor, not an exhaustive count: a
# request for a blob the image lacks fails into kmsg at probe time,
# which the log relay ships, and that case is reportable under the
# same say-what-would-fix-it rule as an unclaimed device.
#
# The blobs ship uncompressed. The system image is a zstd squashfs,
# so the filesystem already compresses and deduplicates them; a
# second compression layer would add machinery and save nothing.
#
# The WHENCE manifest and the license texts ship beside the blobs.
# WHENCE is the per-blob record of provenance and terms, and the
# blobs must not travel without it (licensing/NOTICES.md relies on
# it).
#
# Usage:
#   linux-firmware/derive.sh    derive for the pinned kernel
#
# The result lands in linux-firmware/dist/<version>/lib/firmware,
# with the manifest at linux-firmware/dist/<version>/derived.txt.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in modinfo realpath; do
    command -v "$tool" >/dev/null || {
        echo "derive.sh: missing required tool: $tool" >&2
        exit 1
    }
done

version="$(cat "$here/VERSION")"
tree="$here/cache/$version"
[[ -f "$tree/WHENCE" ]] || {
    echo "derive.sh: no extracted tree at $tree — run fetch.sh first" >&2
    exit 1
}

kernel_version="$(cat "$here/../kernel/VERSION")"
kdist="$here/../kernel/dist/$kernel_version"
release="$(cat "$kdist/release")"
modules="$kdist/lib/modules/$release"
[[ -d "$modules" ]] || {
    echo "derive.sh: no module tree at $modules — build the kernel domain first" >&2
    exit 1
}

out="$here/dist/$version"
fw="$out/lib/firmware"
manifest="$out/derived.txt"
rm -rf "$out"
mkdir -p "$fw"

# The union of declarations, one name per line. modinfo reads the
# compressed modules directly.
names="$(mktemp)"
trap 'rm -f "$names"' EXIT
find "$modules" -name '*.ko.zst' -print0 |
    xargs -0 -P "$(nproc)" -n 64 modinfo -F firmware |
    sort -u >"$names"

# The WHENCE aliases: "Link: <alias> -> <target>", target relative
# to the alias's directory.
links="$(mktemp)"
trap 'rm -f "$names" "$links"' EXIT
awk '/^Link:/ {print $2 " " $4}' "$tree/WHENCE" >"$links"

shipped=0
aliased=0
excluded=0
unshipped=()

# resolve_link prints the tree-relative target of an alias, or
# nothing when the name is no alias.
resolve_link() {
    local name="$1" target
    target="$(awk -v n="$name" '$1 == n {print $2; exit}' "$links")"
    [[ -n "$target" ]] || return 0
    realpath -m --relative-to="$tree" "$tree/$(dirname "$name")/$target"
}

# ship copies one tree-relative file into the output, parents
# included, once.
ship() {
    local file="$1"
    [[ -f "$fw/$file" ]] || {
        mkdir -p "$fw/$(dirname "$file")"
        cp "$tree/$file" "$fw/$file"
    }
}

cd "$tree"
while IFS= read -r name; do
    if [[ "$name" == nvidia/* ]]; then
        excluded=$((excluded + 1))
        continue
    fi
    if [[ -f "$name" ]]; then
        ship "$name"
        shipped=$((shipped + 1))
    elif [[ "$name" == *[*?]* ]]; then
        # A glob can match directories too (variant boards live in
        # subdirectories, requested by names the driver constructs at
        # runtime), so a directory match ships its whole subtree.
        matches="$(compgen -G "$name" || true)"
        if [[ -n "$matches" ]]; then
            while IFS= read -r match; do
                while IFS= read -r file; do
                    ship "$file"
                done < <(find "$match" -type f)
            done <<<"$matches"
            shipped=$((shipped + 1))
        else
            unshipped+=("$name")
        fi
    else
        target="$(resolve_link "$name")"
        if [[ -n "$target" && -f "$target" ]]; then
            ship "$target"
            aliased=$((aliased + 1))
        else
            unshipped+=("$name")
        fi
    fi
done <"$names"

# Materialize every alias whose target shipped. Aliases beyond the
# declared names cost nothing and cover names that drivers construct
# at runtime.
while read -r alias target; do
    resolved="$(realpath -m --relative-to="$tree" "$tree/$(dirname "$alias")/$target")"
    if [[ -f "$fw/$resolved" && ! -e "$fw/$alias" ]]; then
        mkdir -p "$fw/$(dirname "$alias")"
        ln -s "$target" "$fw/$alias"
    fi
done <"$links"

# The ledger and the terms travel with the blobs. Upstream keeps
# every per-family license text in LICENSES/, and WHENCE names which
# text governs which blob.
cp "$tree/WHENCE" "$tree/LICENSE" "$fw/"
cp -r "$tree/LICENSES" "$fw/LICENSES"

{
    echo "linux-firmware $version, derived for kernel $release"
    echo
    echo "shipped: $shipped names as files, $aliased through WHENCE links"
    echo "excluded: $excluded names under nvidia/ (the named exception)"
    echo "unshipped: ${#unshipped[@]} names with no file in this release:"
    printf '  %s\n' "${unshipped[@]}"
} >"$manifest"

echo "firmware for kernel $release:"
du -sh "$fw" | cut -f1 | xargs -I{} echo "  {} from $shipped names, $aliased aliases, ${#unshipped[@]} unshipped (see derived.txt)"
