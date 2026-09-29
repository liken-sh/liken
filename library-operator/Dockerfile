# The operator's image: one static Go binary, the CA bundle every TLS call
# reads, and nothing else. The operator holds the cluster credentials and
# writes every status, so its image carries no shell, no libc, and no other
# tools.
# The two facts that open a media file, the probe with ffprobe and trickplay
# with ffmpeg, run on the image Dockerfile.ffmpeg builds instead, because a
# static ffmpeg loads no VA-API driver. The media browser and the Corrosion
# sidecar build from media-browser/Dockerfile and corrosion/Dockerfile, and the
# release ships the four together.

FROM golang:1.27.0-bookworm AS build
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
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. It runs
# from scratch, where there is no loader to need.
#
# The image carries two builds of the program. /library-operator is the
# full build, and the operator runs it. /library-operator-pod is the pod
# build, with the build tag pod, and every pod and Job the operator
# creates runs it. The pod build leaves out client-go, which the
# operator's watches and its Lease link, so the program each catalog
# pod and each library Job runs is 11.9 MB stripped where the full
# build is 30.8 MB. operate_pod.go says why.
RUN CGO_ENABLED=0 go build -trimpath -o /library-operator . \
    && CGO_ENABLED=0 go build -trimpath -tags pod -o /library-operator-pod .

FROM scratch
COPY --from=build /library-operator /library-operator
COPY --from=build /library-operator-pod /library-operator-pod
# A scratch image carries no trust store, and every TLS call fails without
# one. The bundle is the build stage's own Debian set.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENTRYPOINT ["/library-operator"]
