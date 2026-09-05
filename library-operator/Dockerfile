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

# The franchises scanner clones a git repository, so the image carries
# git. A Library of kind franchises names a url and a ref, and each scan
# Job clones that ref into an emptyDir and exits with the checkout. A
# tarball would drop git from the image, but the archive url differs per
# forge and carries no commit id, so the clone wins. Debian ships no
# static git, so the binary and its musl runtime come from a pinned
# Alpine, the way the ffmpeg tools come from a pinned image.
FROM alpine:3.22 AS git
RUN apk add --no-cache git

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
# git, the helpers it runs for the https transport, and the musl runtime
# they load. git-remote-https is a program of its own under
# /usr/libexec/git-core, and it loads libcurl, so the libraries travel
# with the binary. Alpine's libcurl reads its trust store at
# /etc/ssl/cert.pem, so the same Debian bundle the Go binary reads is
# written there too.
COPY --from=git /usr/bin/git /usr/bin/git
COPY --from=git /usr/libexec/git-core /usr/libexec/git-core
COPY --from=git /usr/share/git-core /usr/share/git-core
COPY --from=git /lib /lib
COPY --from=git /usr/lib /usr/lib
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/cert.pem
# A scratch image carries no PATH, and the file facts find their tools by name.
ENV PATH=/usr/bin
ENTRYPOINT ["/library-operator"]
