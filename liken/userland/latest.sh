#!/usr/bin/env bash
#
# Report the buildroot commit that builds the static programs, the
# version of each program that the build uses, and what each upstream
# has released now.
#
# The buildroot row compares the pinned commit with the tip of
# buildroot's master branch. `liken` builds from source, so no row has
# a channel, and both channel columns print `-`.
#
# Each program row gives the version that the build uses: the version
# in this tree's own recipe, for open-iscsi, kmod, and BlueZ; else the
# version that versions.mk sets; else the version of buildroot's recipe
# at the pinned commit. Under the rule in root plan 83 it follows the
# newest upstream release. A program row reads `behind` when that
# version is older than upstream. The rows cover the programs of both
# builds of this tree, the OS's and the bluetoothd image's.
# The image that the build runs in is the `buildroot` entry in
# toolchains/images.toml, and toolchains/latest.sh reports it against
# the tag that buildroot's CI names at master.
#
# Each bump writes a digest that an upstream signature stands behind
# where the upstream signs one:
#
#   buildroot       GitHub writes the tarball of a commit with
#                   `git archive`, and nothing signs it, so the bump
#                   computes the digest from its own download.
#   e2fsprogs,      kernel.org's checksum autosigner clearsigns the
#   nfs-utils,      sha256sums.asc file of each release directory, and
#   kmod, bluez     trust/keys/kernel-org-digest.sh verifies it before
#                   it prints the digest.
#   open-iscsi      Nothing upstream checksums a GitHub source archive,
#                   so the bump computes the digest from its own
#                   download.
#   dbus            dbus signs each tarball, and the bump verifies it
#                   against dbus.asc beside this script.
#   tzdata          IANA signs each tarball, and the bump verifies the
#                   tzdata and tzcode tarballs against tz.asc beside
#                   this script. buildroot builds zic from tzcode, so
#                   the two move together.
#   wpa-supplicant  w1.fi publishes no checksum file, so the bump
#                   computes the digest from its own download. w1.fi
#                   publishes a detached signature, which the bump does
#                   not check; check it by hand.
#
# Usage:
#   userland/latest.sh                    report every pin
#   userland/latest.sh --bump             move buildroot to master's
#                                         tip in VERSION, fetch.sh, and
#                                         the bluetoothd Dockerfile,
#                                         and delete each line of
#                                         versions.mk that it meets
#   userland/latest.sh --bump <program>   move one program: in its
#                                         recipe under iscsi/ or
#                                         bluetooth/, or else in
#                                         versions.mk

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

require_tools jq sha256sum

commit="$(cat "$here/VERSION")"
versions="$here/versions.mk"

# Each program has the name of its row, which is also its name in the
# OS's components record, and the name of its buildroot package. The
# packages of this tree's own recipes, under iscsi/ and bluetooth/,
# hold their version in their recipe. Every other package takes the
# version of buildroot's recipe, or the one that versions.mk sets.
programs=(e2fsprogs nfs-utils open-iscsi kmod wpa-supplicant tzdata bluez dbus)
declare -A package=(
    [e2fsprogs]=e2fsprogs
    [nfs-utils]=nfs-utils
    [open-iscsi]=open-iscsi-static
    [kmod]=libkmod-static
    [wpa-supplicant]=wpa_supplicant
    [tzdata]=tzdata
    [zic]=zic
    [bluez]=bluez-static
    [dbus]=dbus
)
dockerfile="$here/../../bluetooth-operator/bluetoothd/Dockerfile"

# variable <package> prints the make variable that holds the package's
# version: wpa_supplicant's is WPA_SUPPLICANT_VERSION.
variable() { echo "$1" | tr '[:lower:]-' '[:upper:]_' | sed 's/$/_VERSION/'; }

# recipe_version <commit> <package> prints the version that buildroot's
# recipe names at a commit.
recipe_version() {
    fetch_text "https://raw.githubusercontent.com/buildroot/buildroot/$1/package/$2/$2.mk" |
        sed -n "s/^$(variable "$2") = \([^ ]*\)$/\1/p"
}

# recipe_of <package> prints the path of this tree's recipe of the
# package, or nothing when buildroot's recipe builds it.
recipe_of() {
    local mk
    for mk in "$here"/iscsi/"$1"/"$1".mk "$here"/bluetooth/"$1"/"$1".mk; do
        if [[ -f "$mk" ]]; then echo "$mk"; fi
    done
}

# override_of <package> prints the version that versions.mk sets, or
# nothing.
override_of() {
    sed -n "s/^override $(variable "$1") = \(.*\)$/\1/p" "$versions"
}

# upstream <program> prints the newest release that the program's
# upstream publishes.
upstream() {
    case "$1" in
    e2fsprogs)
        # kernel.org keeps one directory for each release, named
        # v<version>, and release candidates go into testing/ beside them.
        fetch_text "https://www.kernel.org/pub/linux/kernel/people/tytso/e2fsprogs/" |
            grep -oE 'href="v[0-9]+\.[0-9]+(\.[0-9]+)?/"' |
            sed -e 's|href="v||' -e 's|/"$||' | sort -V | tail -1
        ;;
    nfs-utils)
        fetch_text "https://www.kernel.org/pub/linux/utils/nfs-utils/" |
            grep -oE 'href="[0-9]+\.[0-9]+\.[0-9]+/"' |
            sed -e 's|href="||' -e 's|/"$||' | sort -V | tail -1
        ;;
    wpa-supplicant)
        # hostap publishes one tarball for each release in one directory.
        fetch_text "https://w1.fi/releases/" |
            grep -oE 'wpa_supplicant-[0-9]+\.[0-9]+\.tar\.gz' |
            sed -e 's|wpa_supplicant-||' -e 's|\.tar\.gz||' | sort -V | tail -1
        ;;
    tzdata)
        # IANA serves the name of the current release, and nothing else,
        # at one path.
        fetch_text https://data.iana.org/time-zones/tzdb/version | tr -d '[:space:]'
        ;;
    open-iscsi)
        newest_tag https://github.com/open-iscsi/open-iscsi '^[0-9]+\.[0-9]+\.[0-9]+$'
        ;;
    kmod)
        fetch_text https://www.kernel.org/pub/linux/utils/kernel/kmod/ |
            grep -oE 'kmod-[0-9]+(\.[0-9]+)*\.tar\.xz' |
            sed -e 's|kmod-||' -e 's|\.tar\.xz||' | sort -V | tail -1
        ;;
    bluez)
        fetch_text https://mirrors.edge.kernel.org/pub/linux/bluetooth/ |
            grep -oE 'bluez-[0-9]+\.[0-9]+\.tar\.xz' |
            sed -e 's|^bluez-||' -e 's|\.tar\.xz$||' | sort -uV | tail -1
        ;;
    dbus)
        # dbus numbers a development series with an odd minor version,
        # such as 1.15, and a stable series with an even one, so the
        # report reads the even series only.
        fetch_text https://dbus.freedesktop.org/releases/dbus/ |
            grep -oE 'dbus-[0-9]+\.[0-9]+\.[0-9]+\.tar\.xz"' |
            sed -e 's|^dbus-||' -e 's|\.tar\.xz"$||' |
            awk -F. '$2 % 2 == 0' | sort -uV | tail -1
        ;;
    esac
}

tip="$(github_api repos/buildroot/buildroot/commits/master | jq -r .sha)" || tip=""
row buildroot "${commit:0:12}" "${tip:0:12}" - - - "the tip of master"

declare -A used
for program in "${programs[@]}"; do
    pkg="${package[$program]}"
    latest="$(upstream "$program")" || latest=""
    mk="$(recipe_of "$pkg")"
    if [[ -n "$mk" ]]; then
        used[$program]="$(sed -n "s/^$(variable "$pkg") = \(.*\)$/\1/p" "$mk")"
        row "  $program" "${used[$program]}" "$latest" - - - "the recipe in ${mk#"$here"/}"
        continue
    fi
    recipe="$(recipe_version "$commit" "$pkg")" || recipe=""
    override="$(override_of "$pkg")"
    if [[ -n "$override" ]]; then
        used[$program]="$override"
        note="versions.mk sets it; buildroot's recipe names ${recipe:-?}"
    else
        used[$program]="${recipe:-?}"
        note="buildroot's recipe"
    fi
    row "  $program" "${used[$program]}" "$latest" - - - "$note"
done

[[ "${1:-}" == "--bump" ]] || exit 0

# write_override <package> <version> <file> <digest> <source>: set the
# version in versions.mk and write the .hash file that buildroot checks
# the tarball against. <source> says where the digest came from, and it
# becomes the comment of the .hash file.
write_override() {
    local pkg="$1" version="$2" file="$3" digest="$4" source="$5" var dir
    var="$(variable "$pkg")"
    if grep -q "^override $var = " "$versions"; then
        old="$(override_of "$pkg")"
        set_line "$versions" "^override $var = " "override $var = $version"
        rm -rf "${here:?}/patches/$pkg/$old"
    else
        echo "override $var = $version" >>"$versions"
    fi
    dir="$here/patches/$pkg/$version"
    mkdir -p "$dir"
    {
        echo "# The tarball of the version that versions.mk sets. $source"
        echo "sha256  $digest  $file"
    } >"$dir/$pkg.hash"
}

# write_recipe <package> <version> <file> <digest> <source>: set the
# version in this tree's recipe of the package and the tarball's line
# in its .hash file. The license lines of the .hash file stay, and the
# build stops when a new release changed a license file, so a person
# reads the change before the line moves.
write_recipe() {
    local pkg="$1" version="$2" file="$3" digest="$4" source="$5" mk hash
    mk="$(recipe_of "$pkg")"
    hash="${mk%.mk}.hash"
    set_line "$mk" "^$(variable "$pkg") = " "$(variable "$pkg") = $version"
    set_line "$hash" '^# The digest came from ' "# $source"
    set_line "$hash" '^sha256  [0-9a-f]{64}  [^/]*\.tar\.' "sha256  $digest  $file"
}

# drop_overrides deletes each line of versions.mk whose version the
# pinned buildroot recipe meets or passes, with its .hash file.
drop_overrides() {
    local pkg version recipe
    for pkg in "${package[@]}"; do
        version="$(override_of "$pkg")"
        [[ -n "$version" ]] || continue
        recipe="$(recipe_version "$(cat "$here/VERSION")" "$pkg")"
        [[ -n "$recipe" ]] || fail "buildroot has no recipe for $pkg"
        if [[ "$(printf '%s\n%s\n' "$version" "$recipe" | sort -V | tail -1)" == "$recipe" ]]; then
            grep -v "^override $(variable "$pkg") = " "$versions" >"$versions.pin"
            cat "$versions.pin" >"$versions"
            rm "$versions.pin"
            rm -rf "${here:?}/patches/$pkg/$version"
            rmdir "$here/patches/$pkg" 2>/dev/null || true
            echo "versions.mk: buildroot's $pkg $recipe replaces $version"
        fi
    done
}

case "${2:-buildroot}" in
buildroot)
    bump_needed buildroot "$commit" "$tip" || exit 0
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' EXIT
    fetch_file "https://github.com/buildroot/buildroot/archive/$tip.tar.gz" "$work/buildroot.tar.gz"
    digest="$(sha256sum "$work/buildroot.tar.gz" | cut -d' ' -f1)"
    echo "$tip" >"$here/VERSION"
    add_entry "$here/fetch.sh" buildroot_sha256 "$tip" "$digest"
    schema="$(tar -xzOf "$work/buildroot.tar.gz" "buildroot-$tip/utils/generate-cyclonedx" |
        sed -n 's/^DEFAULT_CYCLONEDX_VERSION = "\(.*\)"$/\1/p')"
    if ! grep -qF "[\"$schema\"]=" "$here/fetch.sh"; then
        add_entry "$here/fetch.sh" spdx_schema_sha256 "$schema" \
            "$(digest_of_url "https://raw.githubusercontent.com/CycloneDX/specification/$schema/schema/spdx.schema.json")"
        echo "the SPDX schema $schema came from this download; nothing upstream signs it"
    fi
    set_line "$dockerfile" '^ARG BUILDROOT_COMMIT=' "ARG BUILDROOT_COMMIT=$tip"
    set_line "$dockerfile" '^ARG BUILDROOT_SHA256=' "ARG BUILDROOT_SHA256=$digest"
    bumped buildroot "${commit:0:12}" "${tip:0:12}"
    echo "the digest came from this download; nothing upstream signs it"
    drop_overrides
    next_step "toolchains/latest.sh --bump buildroot when its row reads behind, then make -C .. userland"
    ;;
e2fsprogs | nfs-utils)
    program="$2"
    pkg="${package[$program]}"
    latest="$(upstream "$program")"
    bump_needed "$program" "${used[$program]}" "$latest" || exit 0
    if [[ "$program" == e2fsprogs ]]; then
        sums="https://www.kernel.org/pub/linux/kernel/people/tytso/e2fsprogs/v$latest/sha256sums.asc"
    else
        sums="https://www.kernel.org/pub/linux/utils/nfs-utils/$latest/sha256sums.asc"
    fi
    file="$pkg-$latest.tar.xz"
    digest="$("$here/../../trust/keys/kernel-org-digest.sh" "$sums" "$file")"
    write_override "$pkg" "$latest" "$file" "$digest" \
        "The digest came from sha256sums.asc, signed by kernel.org's checksum autosigner."
    bumped "$program" "${used[$program]}" "$latest"
    echo "the digest came from sha256sums.asc, signed by kernel.org's checksum autosigner"
    next_step "make -C .. userland"
    ;;
open-iscsi)
    latest="$(upstream open-iscsi)"
    bump_needed open-iscsi "${used[open-iscsi]}" "$latest" || exit 0
    digest="$(digest_of_url "https://github.com/open-iscsi/open-iscsi/archive/refs/tags/$latest.tar.gz")"
    write_recipe open-iscsi-static "$latest" "open-iscsi-$latest.tar.gz" "$digest" \
        "The digest came from this download, and nothing upstream signs it."
    bumped open-iscsi "${used[open-iscsi]}" "$latest"
    echo "the digest came from this download; nothing upstream signs it"
    next_step "make -C .. userland"
    ;;
kmod | bluez)
    program="$2"
    latest="$(upstream "$program")"
    bump_needed "$program" "${used[$program]}" "$latest" || exit 0
    if [[ "$program" == kmod ]]; then
        file="kmod-$latest.tar.xz"
        sums=https://www.kernel.org/pub/linux/utils/kernel/kmod/sha256sums.asc
    else
        file="bluez-$latest.tar.xz"
        sums=https://mirrors.edge.kernel.org/pub/linux/bluetooth/sha256sums.asc
    fi
    digest="$("$here/../../trust/keys/kernel-org-digest.sh" "$sums" "$file")"
    write_recipe "${package[$program]}" "$latest" "$file" "$digest" \
        "The digest came from sha256sums.asc, signed by kernel.org's checksum autosigner."
    bumped "$program" "${used[$program]}" "$latest"
    echo "the digest came from sha256sums.asc, signed by kernel.org's checksum autosigner"
    if [[ "$program" == bluez ]]; then
        next_step "docker buildx bake bluetoothd at the top of the repository; the build stops"
        echo "when a patch in bluetooth/bluez-static/ no longer applies, and its README.md says what to do"
    else
        next_step "make -C .. userland"
    fi
    ;;
dbus)
    # dbus signs each release tarball and no checksum file, so the bump
    # verifies the tarball against dbus.asc beside this script, the key
    # of Simon McVittie, who signs the dbus releases, and pins the
    # digest of the verified bytes. It accepts that key only, so a
    # release that another maintainer signs stops the bump until a
    # person checks the new key and adds it.
    latest="$(upstream dbus)"
    bump_needed dbus "${used[dbus]}" "$latest" || exit 0
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' EXIT
    file="dbus-$latest.tar.xz"
    fetch_file "https://dbus.freedesktop.org/releases/dbus/$file" "$work/$file"
    fetch_file "https://dbus.freedesktop.org/releases/dbus/$file.asc" "$work/$file.asc"
    verify_signature --signer DA98F25C0871C49A59EAFF2C4DE8FF2A63C7CC90 "$here/dbus.asc" "$work/$file.asc" "$work/$file"
    write_override dbus "$latest" "$file" "$(sha256sum "$work/$file" | cut -d' ' -f1)" \
        "The digest came from bytes whose signature verified against dbus.asc."
    bumped dbus "${used[dbus]}" "$latest"
    echo "the digest came from the tarball, whose signature verified against dbus.asc"
    next_step "docker buildx bake bluetoothd at the top of the repository"
    ;;
wpa-supplicant)
    latest="$(upstream wpa-supplicant)"
    bump_needed wpa-supplicant "${used[wpa-supplicant]}" "$latest" || exit 0
    file="wpa_supplicant-$latest.tar.gz"
    digest="$(digest_of_url "https://w1.fi/releases/$file")"
    write_override wpa_supplicant "$latest" "$file" "$digest" \
        "The digest came from this download, and nothing upstream signs it."
    bumped wpa-supplicant "${used[wpa-supplicant]}" "$latest"
    echo "the digest came from this download; check w1.fi's .asc signature by hand"
    next_step "make -C .. userland"
    ;;
tzdata)
    latest="$(upstream tzdata)"
    bump_needed tzdata "${used[tzdata]}" "$latest" || exit 0
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' EXIT
    releases="https://data.iana.org/time-zones/releases"
    for pair in tzdata:tzdata zic:tzcode; do
        pkg="${pair%%:*}"
        file="${pair#*:}$latest.tar.gz"
        fetch_file "$releases/$file" "$work/$file"
        fetch_file "$releases/$file.asc" "$work/$file.asc"
        verify_signature "$here/tz.asc" "$work/$file.asc" "$work/$file"
        write_override "$pkg" "$latest" "$file" "$(sha256sum "$work/$file" | cut -d' ' -f1)" \
            "The digest came from bytes whose IANA signature verified against tz.asc."
    done
    bumped tzdata "${used[tzdata]}" "$latest"
    echo "IANA's detached signatures stand behind these bytes"
    next_step "make -C .. userland"
    ;;
*) fail "no pin named $2; try buildroot, ${programs[*]}" ;;
esac
