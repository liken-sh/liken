#!/usr/bin/env bash
#
# Vendor the flux engine seed: the manifests that install Flux's
# floor components, the source and kustomize controllers.
#
# The flux feature follows liken's seed pattern (the plan document
# plans/completed/14-gitops-from-first-boot.md tells the whole story). The
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
# CLI, verifies it against the digest pinned below, which
# flux/latest.sh --bump copies from the checksum file published beside
# it, and renders the seed with it. The CLI is a build tool only; nothing
# ships it. Pinning the CLI version pins the rendered manifests, so
# one VERSION file governs the seed the way every vendored pin works
# here.
#
# Usage:
#   flux/fetch.sh             fetch the version pinned in flux/VERSION
#   flux/fetch.sh v2.9.1      fetch another version that has a digest
#                             pinned below
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

# The digest of the CLI tarball, pinned beside the version it belongs to.
# flux/latest.sh --bump copies it from the checksum file that the
# release publishes, so a fetch never reads a digest from upstream, and
# a file that changes upstream after the bump fails the check.
declare -A flux_sha256=(
    ["v2.9.6"]="b4d22673e9246cbd628881f1a9ef3b090085dced291e42d804555cee8e8d42c5"
)
digest="${flux_sha256[$version]:-}"
if [[ -z "$digest" ]]; then
    echo "fetch.sh: no digest pinned for flux $version; flux/latest.sh --bump writes it" >&2
    exit 1
fi

if ! sha256sum --check --status <<<"$digest  $cache/$tarball" >/dev/null 2>&1; then
    echo "downloading flux $version"
    curl -fL --retry 5 --retry-all-errors --retry-delay 5 -C - \
        --progress-bar -o "$cache/$tarball" "$base/$tarball"
    sha256sum --check --quiet <<<"$digest  $cache/$tarball"
fi
tar -xzf "$cache/$tarball" -C "$cache" flux

# Render the seed. --export writes manifests to stdout and touches no
# cluster. The floor components only; the repository supplies
# everything past the floor. The network policies stay in, because
# flux keeps its defaults until a repository replaces them.
rm -rf "$out"
mkdir -p "$out"
"$cache/flux" install \
    --namespace=flux-system \
    --components=source-controller,kustomize-controller \
    --export >"$out/gotk-components.yaml"

echo
echo "flux $version seed:"
du -sh "$out/gotk-components.yaml"
