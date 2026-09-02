# PROSE: the operator's image: one static Go binary and one static
# ffprobe, and nothing else. Say why the image carries no shell, no
# libc, and no other tools, that the probe concern is the one role that
# opens a media file, and that the media browser and the Corrosion
# sidecar build from media-browser/Dockerfile and corrosion/Dockerfile
# and ship beside it.

FROM golang:1.27.0-bookworm AS build
WORKDIR /src
# The module files come first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. It runs
# from scratch, where there is no loader to need.
RUN CGO_ENABLED=0 go build -trimpath -o /library-operator .

# PROSE: says why ffprobe comes from a pinned upstream image rather than
# a package install: the binaries there are static, so they run from
# scratch with no loader, and the exact tag is what makes two builds of
# one commit read the same file the same way.
FROM mwader/static-ffmpeg:8.0 AS ffmpeg

FROM scratch
COPY --from=build /library-operator /library-operator
COPY --from=ffmpeg /ffprobe /usr/bin/ffprobe
# PROSE: says why the image states its own PATH: a scratch image carries
# none, and the probe role finds ffprobe by name.
ENV PATH=/usr/bin
ENTRYPOINT ["/library-operator"]
