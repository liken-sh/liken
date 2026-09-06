# The operator's image: one static Go binary, two static ffmpeg tools, the CA
# bundle every TLS call reads, and nothing else. The operator holds the
# cluster credentials and writes every status, so its image carries no shell,
# no libc, and no other tools.
# Two facts open a media file: the probe fact reads a file's streams with
# ffprobe, and the trickplay fact decodes a video into thumbnail sheets with
# ffmpeg. The media browser and the
# Corrosion sidecar build from media-browser/Dockerfile and
# corrosion/Dockerfile, and the release ships the three together.

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

# The tools come from a pinned upstream image and not from a package install.
# The binaries there are static, so they run from scratch with no loader, and
# the exact tag is what makes two builds of one commit read the same file the
# same way.
FROM mwader/static-ffmpeg:8.0 AS ffmpeg

FROM scratch
COPY --from=build /library-operator /library-operator
# A scratch image carries no trust store, and every TLS call fails without
# one. The bundle is the build stage's own Debian set.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=ffmpeg /ffprobe /usr/bin/ffprobe
# The trickplay fact decodes a video and tiles its thumbnails, which ffprobe
# cannot do. The static ffmpeg is 133 MiB, the largest thing in the image by
# far, and it ships with the operator and the probe fact because one image
# runs every role.
COPY --from=ffmpeg /ffmpeg /usr/bin/ffmpeg
# A scratch image carries no PATH, and the file facts find their tools by name.
ENV PATH=/usr/bin
ENTRYPOINT ["/library-operator"]
