#!/usr/bin/env bash
#
# Vendors a static wpa_supplicant, the program that runs the 802.11
# association and the WPA handshake for a wireless spec.network entry
# (plans/62-wifi.md).
#
# liken vendors a supplicant instead of writing one. WPA2-PSK alone
# is tractable, EAPOL frames plus netlink key installs, but WPA3 adds
# SAE, a full elliptic-curve exchange, and hostap already owns both.
# The build runs from source inside the pinned container, like
# open-iscsi and nfs-utils, because nobody publishes a static
# wpa_supplicant and this machine has no loader and no shared
# libraries. The extra libraries come from alpine's static packages:
# libnl-3 for nl80211, and OpenSSL for SAE's elliptic-curve math.
#
# The feature list lives in build.config beside this script rather
# than as lines in this file, because upstream's build reads a
# .config and a reviewer should see liken's whole list in one place.
#
# Usage:
#   wpa-supplicant/fetch.sh                   build the version pinned in
#                                             wpa-supplicant/VERSION
#   wpa-supplicant/fetch.sh --sources-only    fetch and verify the source
#                                             tarball, skipping the build
#
# --sources-only exists for the licensing domain: the release channel
# mirrors these same tarballs as the source that corresponds to the
# binary. Mirroring needs the verified bytes, not the build, and it
# has no container runtime.
#
# Results land in wpa-supplicant/dist/<version>/, with the source
# tarball cached in wpa-supplicant/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

sources_only=""
[[ "${1:-}" != "--sources-only" ]] || sources_only=1

for tool in curl sha256sum; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

runtime=""
for candidate in docker podman; do
    if command -v "$candidate" >/dev/null; then
        runtime="$candidate"
        break
    fi
done
[[ -n "$runtime" || -n "$sources_only" ]] || {
    echo "fetch.sh: needs docker or podman to run the pinned build container" >&2
    exit 1
}

version="$(cat "$here/VERSION")"

# Every input is pinned by hash, the same discipline as every vendored
# domain.
# The digest pins the tarball that wpa-supplicant/VERSION names.
# Building another version means moving both together.
builder="docker.io/library/alpine@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce" # 3.22
wpasupplicant_sha256="08e23937e16d0155e55cab2b51f51fbe10d80a1aa91c4e15442645059b737ef6"

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache" "$out"

# Download once, verify every time. If a cached tarball stops matching
# its pin, the build fails instead of using the tarball.
fetch() {
    local url="$1" sha="$2" file="$cache/$3"
    if [[ ! -f "$file" ]]; then
        curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 -C - \
            "$url" -o "$file.partial"
        mv "$file.partial" "$file"
    fi
    echo "$sha  $file" | sha256sum --check --quiet
}

fetch "https://w1.fi/releases/wpa_supplicant-$version.tar.gz" \
    "$wpasupplicant_sha256" "wpa_supplicant-$version.tar.gz"

[[ -z "$sources_only" ]] || exit 0

# The build runs with the sources read-only at /in, the feature list
# at /config, and only dist writable at /out; the script below is
# piped to the container's shell. The source tree holds two programs,
# and this build names only one: hostapd is the access-point side,
# and liken joins networks rather than serving them.
"$runtime" run --rm -i \
    -v "$cache:/in:ro" \
    -v "$here/build.config:/config:ro" \
    -v "$out:/out" \
    -e VERSION="$version" \
    -e HOST_UID="$(id -u)" \
    -e HOST_GID="$(id -g)" \
    "$builder" sh -e <<'BUILD'
apk add --quiet build-base pkgconf linux-headers file \
    libnl3-dev libnl3-static openssl-dev openssl-libs-static
mkdir /build

# Unpack, drop liken's feature list in as .config, and link the one
# program static.
tar xzf "/in/wpa_supplicant-$VERSION.tar.gz" -C /build
cd "/build/wpa_supplicant-$VERSION/wpa_supplicant"
cp /config .config
make -j"$(nproc)" wpa_supplicant LDFLAGS=-static >/dev/null

# A dynamic binary would run in this container and fail on the
# machine, which has no loader. This step refuses to produce one.
strip wpa_supplicant
file wpa_supplicant | grep -q "statically linked" || {
    echo "fetch.sh: wpa_supplicant is not statically linked" >&2
    exit 1
}

# The build asks the binary for its own version because a tarball
# that unpacked to a different release would otherwise ship silently.
./wpa_supplicant -v | grep -q "wpa_supplicant v$VERSION" || {
    echo "fetch.sh: the built binary does not report v$VERSION" >&2
    exit 1
}

install -m 0755 wpa_supplicant /out/wpa_supplicant
chown "$HOST_UID:$HOST_GID" /out/wpa_supplicant
BUILD

echo "wpa_supplicant $version:"
(cd "$out" && sha256sum wpa_supplicant)
