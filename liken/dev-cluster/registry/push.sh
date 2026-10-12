#!/usr/bin/env bash
# Copy images from the laptop into the lab registry, so the dev
# cluster pulls an image that you built a minute ago without a round
# trip through ghcr.io.
#
#   registry/push.sh observatory-operator
#   registry/push.sh ghcr.io/liken-sh/indi-simulators:20261005-5
#
# Each argument is either a target of the repository's
# docker-bake.hcl or an image reference that the local docker daemon
# already holds. The script tells them apart by the slash: a target
# name never contains one, and an image reference always does. Only
# references on ghcr.io are accepted, because the dev cluster mirrors
# only ghcr.io through this registry.
#
# A target builds with `docker buildx bake <target> --load` from the
# top of the repository, the same build that CI runs, and then every
# tag that the target declares goes to the registry. VERSION defaults
# to "dev", so an operator image lands as
# ghcr.io/liken-sh/<name>:dev. CI publishes development builds under
# longer tags (2026.10.02-001-dev-017-abcdef01), so ghcr.io has no
# :dev tag. A manifest that names that tag therefore pulls only from
# the lab registry, and never gets an older image from ghcr.io by
# mistake. Set VERSION to push under a different tag. A pinned base,
# such as indi or mpv, keeps the version from its own package.toml.
#
# The build reads no layer cache from ghcr.io. The bake file names
# a cache on ghcr.io for each target, and on a slow uplink the
# download of that cache costs more than a local build. The local
# BuildKit cache still applies.
#
# BAKE_FLAGS passes more flags to the build. When the build's
# containers cannot resolve names, for example on a VPN whose DNS
# server answers the laptop but not its containers, run the build on
# the laptop's own network:
#
#   BAKE_FLAGS="--set *.network=host --allow network.host" \
#       registry/push.sh observatory-operator
#
# The registry keeps each image under the path it has on ghcr.io:
# ghcr.io/liken-sh/<name>:<tag> becomes
# localhost:5000/liken-sh/<name>:<tag>. containerd asks a mirror for
# the repository path of the original reference, so this layout is
# what the dev cluster's mirror for ghcr.io (cluster.yaml,
# spec.registries) expects to find.
set -euo pipefail

if [ $# -eq 0 ]; then
	echo "usage: $0 <bake target | image reference>..." >&2
	exit 2
fi

repo=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
registry=${LAB_REGISTRY:-localhost:5000}
export VERSION=${VERSION:-dev}

# BAKE_FLAGS is a list of flags, so it splits on spaces into words.
read -ra bake_flags <<<"${BAKE_FLAGS:-}"

# The registry serves plain HTTP on the laptop's loopback, so skopeo
# must not ask it for TLS.
push() {
	local ref=$1
	local dest=$registry/${ref#ghcr.io/}
	echo "push: $ref -> $dest"
	skopeo copy --quiet --dest-tls-verify=false \
		"docker-daemon:$ref" "docker://$dest"
}

for arg in "$@"; do
	case $arg in
	ghcr.io/*)
		push "$arg"
		;;
	*/*)
		echo "$arg: the lab registry mirrors only ghcr.io" >&2
		exit 2
		;;
	*)
		(cd "$repo" && docker buildx bake "$arg" --load --set '*.cache-from=' "${bake_flags[@]}")
		for tag in $(cd "$repo" && docker buildx bake "$arg" --print 2>/dev/null |
			jq -r --arg t "$arg" '.target[$t].tags[]'); do
			push "$tag"
		done
		;;
	esac
done
