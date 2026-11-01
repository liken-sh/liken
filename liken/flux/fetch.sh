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
# CLI, verifies it against the digest in flux/pin.toml, which
# flux/latest.sh --bump copies from the checksum file published beside
# it, and renders the seed with it. The CLI is a build tool only; nothing
# ships it. Pinning the CLI version pins the rendered manifests, so
# one VERSION file governs the seed the way every vendored pin works
# here.
#
# Usage:
#   flux/fetch.sh             fetch the version pinned in flux/VERSION
#
# Results land in flux/dist/<version>/gotk-components.yaml, with the
# CLI cached in flux/cache/.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=../../toolchains/pin.sh
source "$here/../../toolchains/pin.sh"

require_tools sha256sum tar

arch="amd64"
version="$(cat "$here/VERSION")"
# Release artifacts name the version without the leading v.
bare="${version#v}"
tarball="flux_${bare}_linux_${arch}.tar.gz"

cache="$here/cache/$version"
out="$here/dist/$version"

# flux/latest.sh --bump copies the digest from the checksum file that
# the release publishes, after the file's sigstore signature verifies
# as the work of Flux's release workflow.
fetch_pinned --resume flux "$version" "$cache/$tarball"
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
