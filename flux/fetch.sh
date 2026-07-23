#!/usr/bin/env bash
#
# Vendor the flux engine seed: the manifests that install Flux's
# floor components, the source and kustomize controllers.
#
# The flux feature follows liken's seed pattern (the plan document
# plans/14-gitops-from-first-boot.md tells the whole story). The
# image plants the engine exactly once, and the git repository owns
# it from then on: the repository carries its own copy of these
# manifests inside the synced path, so the first sync upgrades the
# engine to whatever the repository pins, and every later engine
# change is a commit. This seed only has to be good enough to reach
# that first sync. The seed deliberately carries only the floor. A
# component beyond the floor (the helm-controller, for example) is
# the repository's to add, and a seed that carried more would leave
# orphans behind: the sync only prunes objects it applied itself.
#
# Flux publishes no standalone manifest artifact for a component
# subset. The manifests live inside the flux CLI, and the CLI renders
# them offline (`flux install --export`). So this script fetches the
# CLI, verifies it against the checksum manifest published beside it,
# and renders the seed with it. The CLI is a build tool only; nothing
# ships it. Pinning the CLI version pins the rendered manifests, so
# one VERSION file governs the seed the way every vendored pin works
# here.
#
# Usage:
#   flux/fetch.sh             fetch the version pinned in flux/VERSION
#   flux/fetch.sh v2.9.1      fetch a specific version instead
#
# Results land in flux/dist/<version>/gotk-components.yaml, with the
# CLI cached in flux/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in curl sha256sum tar; do
    command -v "$tool" >/dev/null || {
        echo "fetch.sh: missing required tool: $tool" >&2
        exit 1
    }
done

arch="amd64"
version="${1:-$(cat "$here/VERSION")}"
# Release artifacts name the version without the leading v.
bare="${version#v}"

base="https://github.com/fluxcd/flux2/releases/download/$version"
tarball="flux_${bare}_linux_${arch}.tar.gz"

cache="$here/cache/$version"
out="$here/dist/$version"
mkdir -p "$cache"

digest="$(curl -fsSL "$base/flux_${bare}_checksums.txt" | awk -v f="$tarball" '$2 == f { print $1 }')"
if [[ -z "$digest" ]]; then
    echo "fetch.sh: no $tarball listed in $base/flux_${bare}_checksums.txt" >&2
    exit 1
fi

if ! sha256sum --check --status <<<"$digest  $cache/$tarball" >/dev/null 2>&1; then
    echo "downloading flux $version"
    curl -fL --progress-bar -o "$cache/$tarball" "$base/$tarball"
    sha256sum --check --quiet <<<"$digest  $cache/$tarball"
fi
tar -xzf "$cache/$tarball" -C "$cache" flux

# Render the seed. --export writes manifests to stdout and touches no
# cluster. The floor components only; the repository decides
# everything past the floor. The network policies stay in, because
# the engine should hold its default posture until a repository
# decides otherwise.
rm -rf "$out"
mkdir -p "$out"
"$cache/flux" install \
    --namespace=flux-system \
    --components=source-controller,kustomize-controller \
    --export >"$out/gotk-components.yaml"

echo
echo "flux $version seed:"
du -sh "$out/gotk-components.yaml"
