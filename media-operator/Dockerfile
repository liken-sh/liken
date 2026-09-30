# The operator's image: one static Go binary and nothing else. The
# operator holds the cluster credentials and writes every status, so
# its image carries no shell, no libc, and no tools, which is the
# least there is to attack.
#
# The player image (mpv under this same program as its supervisor) and
# the idle image build from the two Dockerfiles beside this one. The
# sidecar image is this image under a second name: release.yaml tags
# it, and no Dockerfile builds it. A release ships all four together.
#
# The image holds two builds of the program. /media-operator is the
# full build, and the operator runs it. /media-operator-pod is the pod
# build, with the build tag pod, and every sidecar container the
# operator creates runs it. The pod build leaves out client-go's leader
# election, which the operator alone needs, so the program that runs in
# every playback pod and every Remote's pod is less than half the size
# and takes less than half the memory at start. leader_pod.go says why.
# The sidecar image is this image, so each node that runs a sidecar
# pulls both builds, about 40MB where one build was 17MB.

FROM golang:1.27.1-bookworm AS build
WORKDIR /src
# The module files come first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
# go.mod replaces the shared kubernetes module with ../kubernetes, and
# the build takes that directory as a second build context named
# kubernetes. It lands at /kubernetes, the same place relative to /src
# that kubernetes/ has relative to this component. Its module file
# comes first for the same reason as this module's.
COPY --from=kubernetes go.mod go.sum /kubernetes/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
COPY *.go ./
# The api role embeds the OpenAPI document at build time, so the
# committed copy has to be in the tree the compiler sees.
COPY openapi.json ./
# The EDL package builds into the binary, so the source tree it needs is the
# root files and this one directory.
COPY edl/ ./edl/
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. It runs
# from scratch, where there is no loader to need.
RUN CGO_ENABLED=0 go build -trimpath -o /media-operator . \
    && CGO_ENABLED=0 go build -trimpath -tags pod -o /media-operator-pod .

FROM scratch
COPY --from=build /media-operator /media-operator
COPY --from=build /media-operator-pod /media-operator-pod
ENTRYPOINT ["/media-operator"]
