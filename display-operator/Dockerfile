# This file builds three images from one Go binary.
#
#   --target display-operator
#       ghcr.io/liken-sh/display-operator: the weston image plus the
#       binary. The DaemonSet runs it.
#   --target display-capture
#       ghcr.io/liken-sh/display-capture: the ffmpeg image plus the
#       binary. It is the capture container in the operator's pod.
#   --target display-api
#       ghcr.io/liken-sh/display-api: the binary on `scratch`.
#
# weston and ffmpeg are base images of this repository, and the bake
# file passes the build of this commit's recipe of each as a named
# context. The operator image builds on the weston image, so a node that
# runs the compositor and Wayland clients built on the same base stores
# glibc, libdrm, and LLVM once.

FROM golang AS build
# proxy.golang.org can drop a module download partway through. With
# the default comma, `go` falls back to the origin only when the proxy
# returns 404 or 410, so the build fails. With a pipe, `go` falls back
# after any proxy error. `GOSUMDB` keeps its default, so the checksum
# database still verifies a module fetched from its origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
COPY go.mod go.sum ./
# go.mod replaces the kubernetes module with ../kubernetes, so the
# build context kubernetes is copied to /kubernetes, the same path
# relative to /src. The module files are copied before the source so
# that a source edit reuses the cached download layer.
COPY --from=kubernetes go.mod go.sum /kubernetes/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
COPY *.go ./
# The version goes into the liken_build_info metric.
ARG VERSION=dev
# The display-api image has no libc and no dynamic loader, so the
# binary must be static.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /display-operator .
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

FROM weston AS display-operator
# The argument in the manifest selects the role of each container in
# the pod: write the configuration, exec the compositor, run the
# capture server, or, with no argument, run the DRA driver.
COPY --from=build /display-operator /display-operator
COPY --from=build /notices /

ENTRYPOINT ["/display-operator"]

# The capture container runs ffmpeg for each request, as a Wayland
# client of the compositor container in the same pod.
FROM ffmpeg AS display-capture
COPY --from=build /display-operator /display-operator
COPY --from=build /notices /

ENTRYPOINT ["/display-operator"]

# The API only calls the Kubernetes API and the capture containers, so
# it needs no library and the image has no other software to patch.
FROM scratch AS display-api
COPY --from=build /display-operator /display-operator
COPY --from=build /notices /
# deploy/api.yaml runs the API as this user.
USER 65532:65532
ENTRYPOINT ["/display-operator"]
