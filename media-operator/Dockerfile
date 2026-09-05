# The operator's image: one static Go binary and nothing else. The
# operator holds the cluster credentials and writes every status, so
# its image carries no shell, no libc, and no tools, which is the
# least there is to attack.
#
# The player image (mpv under this same binary as its supervisor) and
# the idle image build from the two Dockerfiles beside this one. The
# sidecar image is this image under a second name: release.yaml tags
# it, and no Dockerfile builds it. A release ships all four together.

FROM golang:1.27.0-bookworm AS build
WORKDIR /src
# The module files come first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# The EDL package builds into the binary, so the source tree it needs is the
# root files and this one directory.
COPY edl/ ./edl/
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. It runs
# from scratch, where there is no loader to need.
RUN CGO_ENABLED=0 go build -trimpath -o /media-operator .

FROM scratch
COPY --from=build /media-operator /media-operator
ENTRYPOINT ["/media-operator"]
