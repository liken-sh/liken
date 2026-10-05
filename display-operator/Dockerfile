# Three images from one file, all of them the operator's one binary.
#
#   --target display-operator
#                      ghcr.io/liken-sh/display-operator, the weston
#                      image plus the operator's binary. The DaemonSet
#                      runs it.
#   --target display-capture
#                      ghcr.io/liken-sh/display-capture, the ffmpeg
#                      image plus the same binary, which is the
#                      capture container of the operator's pod.
#   --target display-api
#                      ghcr.io/liken-sh/display-api, the same binary
#                      on nothing else, which is the whole of the
#                      API's image.
#
# weston and ffmpeg are base images of the repository, each in its own
# directory at the top. The bake file at the top of the repository
# gives this build each one as a named context, so FROM weston builds
# on the weston image of the same commit, never on a published tag. The
# compositor that the pod runs is then the same set of bytes that the
# weston image holds, and a node that draws with the compositor and
# the clients holds glibc, libdrm and LLVM once.

FROM golang:1.27.1-bookworm AS build
# proxy.golang.org sometimes drops a module download partway through
# the stream. With the default comma, `go` tries the origin only when
# the proxy answers 404 or 410, so a dropped stream fails the build.
# With a pipe, `go` fetches the module from its origin after any proxy
# error, with the git that this image includes. `GOSUMDB` keeps its
# default, so the checksum database still verifies a module that `go`
# fetched from its origin.
ENV GOPROXY="https://proxy.golang.org|direct"
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
# The version reaches the binary through -ldflags, so a running
# operator's liken_build_info names the release it was built from,
# not the "dev" default main.go carries.
ARG VERSION=dev
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. The binary
# is the whole of the operator image, so it also has to run with no
# loader and no libc under it.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=${VERSION}" -o /display-operator .
# The license files of the Go standard library and of each module that
# the binary links. notices/go.sh says why the image needs them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

FROM weston AS display-operator
# The operator's binary is the entrypoint of every container of the
# pod. The argument the manifest passes selects the role: the config
# write, the compositor it execs, the capture server, or, with no
# argument, the DRA driver.
COPY --from=build /display-operator /usr/local/bin/display-operator
COPY --from=build /notices /

ENTRYPOINT ["/usr/local/bin/display-operator"]

# The capture sidecar runs an ffmpeg process for every request, so
# its image is the ffmpeg image plus the binary. It carries no
# compositor: the sidecar is a Wayland client of the weston container
# beside it.
FROM ffmpeg AS display-capture
COPY --from=build /display-operator /usr/local/bin/display-operator
COPY --from=build /notices /

ENTRYPOINT ["/usr/local/bin/display-operator"]

# The API talks to the Kubernetes API and to the sidecars, and opens
# no library and no device, so the static binary is the whole image
# and there is nothing else in it to patch.
FROM scratch AS display-api
COPY --from=build /display-operator /usr/local/bin/display-operator
COPY --from=build /notices /

ENTRYPOINT ["/usr/local/bin/display-operator"]
