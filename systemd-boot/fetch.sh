#!/usr/bin/env bash
#
# Vendor systemd-boot: the boot menu on liken's install media.
#
# Installed liken machines have no bootloader at all — their firmware
# boots the kernel's own EFI stub straight from a boot entry that
# carries the whole command line. Install media can't do that: when
# firmware boots a removable disk it runs one well-known file
# (\EFI\BOOT\BOOTX64.EFI) with no arguments, and a kernel started
# with no arguments doesn't even know its initramfs exists. Something
# on the stick has to supply the command line, and on liken's stick
# that something is also the install menu: one entry per machine in
# the deployment, so the operator picks the machine they're standing
# at and that entry's options carry liken.machine=<name>.
#
# systemd-boot is the smallest program that does exactly this. It is
# a single ~130KB EFI application, not a bootloader in the GRUB
# sense: no modules, no scripting language, no filesystem drivers of
# its own. It can be this small because the firmware already knows
# how to read FAT and load EFI binaries — systemd-boot only draws a
# menu from the plain-text entry files in /loader/entries on the disk
# it booted from, and chain-loads the chosen kernel's own EFI stub
# with the options that entry names. Several `initrd` lines in an
# entry are concatenated in order, the same composition installed
# machines get from their two initrd= parameters.
#
# The binary is vendored the way the kernel is: prebuilt from
# Ubuntu's archive, pinned by version and digest. Ubuntu's pool only
# carries the current build of each series — when this version is
# superseded, the URL 404s, and the fix is the ordinary one-line
# re-pin: put the new version in systemd-boot/VERSION and the new
# digest below (apt-cache show systemd-boot-efi prints both).
#
# Usage:
#   systemd-boot/fetch.sh    fetch the version pinned in VERSION
#
# Results land in systemd-boot/dist/<version>/systemd-bootx64.efi,
# cached in systemd-boot/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl ar tar zstd sha256sum; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

version="${1:-$(cat "$here/VERSION")}"

# The deb's digest, pinned beside the version it belongs to. The
# Ubuntu archive publishes per-file digests only inside its signed
# Release indexes, not beside the files, so the pin lives here where
# a version bump has to update it deliberately.
declare -A deb_sha256=(
    ["259.5-0ubuntu3"]="068d9c2f0c450c47869669367738057a604e5b8bab3b194ec7808c6e6e712ca6"
)
digest="${deb_sha256[$version]:-}"
if [[ -z "$digest" ]]; then
    echo "fetch.sh: no pinned digest for systemd-boot-efi $version; add it to deb_sha256" >&2
    exit 1
fi

deb="systemd-boot-efi_${version}_amd64.deb"
url="http://archive.ubuntu.com/ubuntu/pool/universe/s/systemd/$deb"

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache"

if ! sha256sum --check --status <<<"$digest  $cache/$deb" >/dev/null 2>&1; then
    echo "downloading systemd-boot-efi $version"
    curl -fL --progress-bar -o "$cache/$deb" "$url"
    sha256sum --check --quiet <<<"$digest  $cache/$deb"
fi

# A .deb is an `ar` archive wrapping a compressed tarball of the
# files; the EFI binary is the only file liken wants from it.
staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT
ar p "$cache/$deb" data.tar.zst | tar --zstd -x -C "$staging" \
    ./usr/lib/systemd/boot/efi/systemd-bootx64.efi

rm -rf "$out"
mkdir -p "$out"
cp "$staging/usr/lib/systemd/boot/efi/systemd-bootx64.efi" "$out/systemd-bootx64.efi"

echo
echo "systemd-boot $version:"
stat -c '%s bytes' "$out/systemd-bootx64.efi"
