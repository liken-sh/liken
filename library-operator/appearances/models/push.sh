#!/usr/bin/env bash
# Pushes the face models of `appearances` to a registry as one OCI
# artifact, which the build of the library-operator-appearances image
# pulls by digest. The weights stay out of the repository's history:
# they are 39 MB of binary files that no build step changes.
#
# Usage: push.sh [repository]
#
#   [repository]  where to push, with no tag. The default is
#                 ghcr.io/liken-sh/library-operator-appearances-models.
#
# Set PLAIN_HTTP=true to push to a registry that serves plain HTTP,
# such as a test registry on localhost:5000.
#
# The script fetches the two models and their license files from OpenCV
# Zoo at one commit, checks every file against models.sha256, adds
# NOTICE, and pushes. The files and the manifest annotations are the same
# on every push, so the manifest digest is the same on every registry,
# and the script fails when it differs from the digest in the file
# digest beside it. The image build pulls that digest, so a push that
# passes here is the artifact the build reads.
#
# oras runs from its own image when no oras is installed. The container
# reads the registry credentials from ~/.docker/config.json.
set -euo pipefail

repository=${1:-ghcr.io/liken-sh/library-operator-appearances-models}
plain_http=${PLAIN_HTTP:-false}
here=$(cd "$(dirname "$0")" && pwd)

# The tag names the two model versions, so a person who lists the
# repository reads which models each artifact holds. The build reads
# the digest, not the tag.
tag=yunet-2023mar-sface-2021dec

# OpenCV Zoo keeps the ONNX files in Git LFS, so the raw URL of a model
# returns the LFS pointer, and the media URL returns the file.
zoo=47534e27c9851bb1128ccc0102f1145e27f23f98
media=https://media.githubusercontent.com/media/opencv/opencv_zoo/$zoo/models
raw=https://raw.githubusercontent.com/opencv/opencv_zoo/$zoo/models

oras_image=ghcr.io/oras-project/oras:v1.3.4@sha256:f7bc056d54d97baa399414ed5048ecc67c3371b750d4bbce1d871827a5758179

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fetch() {
	curl --fail --silent --show-error --location --retry 3 --output "$work/$2" "$1"
}
fetch "$media/face_detection_yunet/face_detection_yunet_2023mar.onnx" face_detection_yunet_2023mar.onnx
fetch "$raw/face_detection_yunet/LICENSE" face_detection_yunet_2023mar.LICENSE
fetch "$media/face_recognition_sface/face_recognition_sface_2021dec.onnx" face_recognition_sface_2021dec.onnx
fetch "$raw/face_recognition_sface/LICENSE" face_recognition_sface_2021dec.LICENSE
cp "$here/NOTICE" "$work/NOTICE"
(cd "$work" && sha256sum --check --strict "$here/models.sha256")

run_oras() {
	if command -v oras >/dev/null; then
		(cd "$work" && oras "$@")
	else
		docker run --rm --network host \
			--volume "$work:/workspace" \
			--volume "$HOME/.docker/config.json:/root/.docker/config.json:ro" \
			"$oras_image" "$@"
	fi
}

# oras writes the time of the push into org.opencontainers.image.created
# unless the push names a time. A fixed time keeps the manifest digest
# the same on every push. The source annotation links the package on
# ghcr.io to this repository.
digest=$(run_oras push \
	--plain-http="$plain_http" \
	--artifact-type application/vnd.liken.appearances.models.v1 \
	--annotation org.opencontainers.image.created=2026-10-01T00:00:00Z \
	--annotation org.opencontainers.image.source=https://github.com/liken-sh/liken \
	--annotation org.opencontainers.image.licenses="MIT AND Apache-2.0" \
	--annotation org.opencontainers.image.description="YuNet and SFace from OpenCV Zoo, for appearances" \
	--format go-template='{{.digest}}' \
	"$repository:$tag" \
	face_detection_yunet_2023mar.onnx:application/octet-stream \
	face_detection_yunet_2023mar.LICENSE:text/plain \
	face_recognition_sface_2021dec.onnx:application/octet-stream \
	face_recognition_sface_2021dec.LICENSE:text/plain \
	NOTICE:text/plain)

echo "pushed $repository:$tag@$digest"
pinned=$(cat "$here/digest")
if [ "$digest" != "$pinned" ]; then
	echo "push.sh: the pushed digest $digest differs from $pinned in $here/digest" >&2
	exit 1
fi
