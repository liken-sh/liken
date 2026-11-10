#!/usr/bin/env bash
# Runs the mpv image once before it ships.
#
# Usage: mpv.sh <image>
#
#   <image>  the full reference of the mpv image, for example
#            ghcr.io/liken-sh/mpv:20260928-1. The image must be in
#            the local Docker daemon.
#
# mpv opens the PipeWire client modules, the SPA plugins, and the
# fontconfig configuration by name, so a file the image lacks shows up
# at the first run and at no earlier point. The runner has no GPU, no
# compositor, and no sound server, so the check proves what it can
# without them. mpv must be the release that the Dockerfile pins, and
# its lists of outputs and decoders must name the three that the
# player selects: --vo=dmabuf-wayland, --ao=pipewire, and
# --hwdec=vaapi (playerArgv in media-operator/player.go). Then mpv
# plays a clip with a video, an AC-3 audio, and a subtitle track to no
# display and no audio, with the options that the player passes for a
# film. The driver load is proved on hardware.
#
# The image's own ffmpeg encodes the clip to a pipe, and mpv reads the
# pipe, so no container writes a file into the runner's directory.
# Each listing writes to a file, because grep -q on a pipe would fail
# the check.
set -euo pipefail

image=$1

# The image must hold the notices of the third-party software in it,
# as notices/check.sh describes. mpv comes from its release, not from
# a Debian package, so its license and the SBOM record that names it
# for syft are named here.
"$(dirname "$0")/../../notices/check.sh" "$image" usr/share/doc/mpv/Copyright \
  usr/share/doc/liken/mpv.cdx.json

version=$(sed -n 's/^ARG MPV_VERSION=//p' "$(dirname "$0")/../Dockerfile")

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# The image has no /tmp, and the IPC socket needs a directory that mpv
# can write, as the player's pod gives it one.
mpv() {
  docker run --rm -i --tmpfs /tmp --entrypoint /usr/bin/mpv "$image" --no-config "$@"
}

mpv --version > "$work/version.txt"
cat "$work/version.txt"
grep -q "^mpv v$version " "$work/version.txt"

# The reset of the spdif muxer, upstream commit 1388a453950f, is what
# keeps a TrueHD bitstream playing after a seek. Its warning text is in
# the binary only when the build compiled the reset, from the patch in
# patches/ now and from the release from 0.42.0 on.
id=$(docker create "$image")
docker cp "$id:/usr/bin/mpv" "$work/mpv" > /dev/null
docker rm "$id" > /dev/null
grep -q 'Still waiting for TrueHD major sync' "$work/mpv"

mpv --vo=help > "$work/vo.txt"
grep -qE '^ +dmabuf-wayland ' "$work/vo.txt"
mpv --ao=help > "$work/ao.txt"
grep -qE '^ +pipewire ' "$work/ao.txt"
mpv --hwdec=help > "$work/hwdec.txt"
grep -qE '^ +vaapi ' "$work/hwdec.txt"

printf '1\n00:00:00,000 --> 00:00:02,000\nliken\n' > "$work/subtitle.srt"
docker run --rm --entrypoint /usr/bin/ffmpeg \
  -v "$work/subtitle.srt:/subtitle.srt:ro" "$image" \
  -nostdin -loglevel error \
  -f lavfi -i testsrc=duration=2:size=64x64:rate=5 \
  -f lavfi -i sine=duration=2:sample_rate=48000 \
  -i /subtitle.srt \
  -map 0 -map 1 -map 2 -c:v mpeg2video -c:a ac3 -c:s ass \
  -f matroska - \
  | mpv --vo=null --ao=null --frames=5 --sid=1 --audio-spdif=ac3,truehd \
    --input-ipc-server=/tmp/mpv.sock --tls-verify=yes - \
  > "$work/play.txt" 2>&1
cat "$work/play.txt"
# The run selected the subtitle track, passed the AC-3 track through as
# an spdif stream, and opened the IPC socket. The image holds no font
# file, so libass reports that it loaded no font, and the player image
# adds the fonts.
grep -q 'Subs.*--sid=1.*ass' "$work/play.txt"
grep -q 'AO: \[null\].*spdif-ac3' "$work/play.txt"
if grep -q 'Could not bind IPC socket' "$work/play.txt"; then
  exit 1
fi
