#!/usr/bin/env bash
# Pushes the text models of `titles` to a registry as one OCI artifact,
# which the build of the library-operator-titles image pulls by digest.
# The weights stay out of the repository's history: they are 13 MB of
# binary files that no build step changes.
#
# Usage: push.sh [repository]
#
#   [repository]  where to push, with no tag. The default is
#                 ghcr.io/liken-sh/library-operator-titles-models.
#
# Set PLAIN_HTTP=true to push to a registry that serves plain HTTP,
# such as a test registry on localhost:5000.
#
# The script fetches the two models, the reader's character list, and
# their license files from the URLs that pin.toml beside it names, gives
# each file the name that `titles read` opens it by, checks every file
# against models.sha256, adds NOTICE, and pushes. The files and the
# manifest annotations are the same on every push, so the manifest
# digest is the same on every registry, and the script fails when it
# differs from the digest in the file digest beside it. The image build
# pulls that digest, so a push that passes here is the artifact the
# build reads.
#
# oras runs from its own image when no oras is installed. The image is
# the `oras` entry of toolchains/images.toml, which the image build of
# library-operator-titles pulls the artifact with too, so one entry
# pins oras for both, and toolchains/latest.sh reports it. The
# container reads the registry credentials from ~/.docker/config.json.
set -euo pipefail

repository=${1:-ghcr.io/liken-sh/library-operator-titles-models}
plain_http=${PLAIN_HTTP:-false}
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source-path=SCRIPTDIR source=../../../toolchains/pin.sh
source "$here/../../../toolchains/pin.sh"

# The tag names the two model versions, so a person who lists the
# repository reads which models each artifact holds. The build reads
# the digest, not the tag.
tag=ppocrv3-det-2023may-ppocrv4-rec

# The license files come from the commits that pin.toml's URLs name:
# the LICENSE of the detector's directory in OpenCV Zoo, and the LICENSE
# of PaddleOCR, which covers the reader and its character list.
zoo=https://raw.githubusercontent.com/opencv/opencv_zoo/47534e27c9851bb1128ccc0102f1145e27f23f98
paddle=https://raw.githubusercontent.com/PaddlePaddle/PaddleOCR/dab3fe35379033fdcb2d0e9572fac0b36c9a9ebf

oras_image=$(image_of oras)

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fetch() {
	curl --fail --silent --show-error --location --retry 3 --output "$work/$2" "$1"
}
fetch "$(pin_value text_detection_en_ppocrv3 url "$here/pin.toml")" text_detection_en_ppocrv3_2023may.onnx
fetch "$zoo/models/text_detection_ppocr/LICENSE" text_detection_en_ppocrv3_2023may.LICENSE
fetch "$(pin_value text_recognition_ch_ppocrv4 url "$here/pin.toml")" text_recognition_ch_ppocrv4.onnx
fetch "$paddle/LICENSE" text_recognition_ch_ppocrv4.LICENSE
fetch "$(pin_value text_recognition_ch_ppocrv4_characters url "$here/pin.toml")" text_recognition_ch_ppocrv4.txt
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

# These are the OCI keys that ci publish writes on every image of the
# repository, less the two that would change on each commit. oras
# writes the time of the push into org.opencontainers.image.created
# unless the push names a time, so a fixed time keeps the manifest
# digest the same on every push, and there is no revision key, because
# the files come from the models' projects and not from a commit of
# this repository. The source annotation links the package on ghcr.io
# to this repository.
digest=$(run_oras push \
	--plain-http="$plain_http" \
	--artifact-type application/vnd.liken.titles.models.v1 \
	--annotation org.opencontainers.image.created=2026-11-01T00:00:00Z \
	--annotation org.opencontainers.image.title=library-operator-titles-models \
	--annotation org.opencontainers.image.version="$tag" \
	--annotation org.opencontainers.image.source=https://github.com/liken-sh/liken \
	--annotation org.opencontainers.image.url=https://liken.sh/ \
	--annotation org.opencontainers.image.documentation=https://liken.sh/library/ \
	--annotation org.opencontainers.image.vendor=liken \
	--annotation org.opencontainers.image.licenses="Apache-2.0" \
	--annotation org.opencontainers.image.description="The PP-OCRv3 text detector and the PP-OCRv4 text reader, for titles" \
	--format go-template='{{.digest}}' \
	"$repository:$tag" \
	text_detection_en_ppocrv3_2023may.onnx:application/octet-stream \
	text_detection_en_ppocrv3_2023may.LICENSE:text/plain \
	text_recognition_ch_ppocrv4.onnx:application/octet-stream \
	text_recognition_ch_ppocrv4.LICENSE:text/plain \
	text_recognition_ch_ppocrv4.txt:text/plain \
	NOTICE:text/plain)

echo "pushed $repository:$tag@$digest"
pinned=$(cat "$here/digest")
if [ "$digest" != "$pinned" ]; then
	echo "push.sh: the pushed digest $digest differs from $pinned in $here/digest" >&2
	exit 1
fi
