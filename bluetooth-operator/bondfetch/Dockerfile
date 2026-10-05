# The bondfetch image: one static binary on an empty filesystem.
#
# It runs as a plain init container, restores the adapter's bonds from
# that adapter's Secret, and exits. bluetoothd reads its bonds once, at
# adapter registration, so this has to finish before the daemon starts.
#
# Nothing else belongs here. It reads the adapter's address from a
# socket, reaches the API server at the address KUBERNETES_SERVICE_HOST
# names over TLS it verifies against the CA the kubelet mounts, and
# writes files into a mounted directory. So the image needs no
# certificate store, no resolver configuration, and no zoneinfo. It
# runs as uid 65534 with every capability dropped, which the manifest
# states.

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
# that kubernetes/ has relative to this component. bondfetch reads the
# API server through its apiclient package.
COPY --from=kubernetes go.mod go.sum /kubernetes/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
COPY bonds/ ./bonds/
COPY bondfetch/ ./bondfetch/
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. -s -w drop
# the symbol table and the DWARF sections, and Go reads panic traces
# from its own pclntab, which stays.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /bondfetch ./bondfetch
# The license files of the Go standard library and of each module that
# the binary links. notices/go.sh says why the image needs them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices ./bondfetch

FROM scratch
COPY --from=build /bondfetch /bondfetch
COPY --from=build /notices /
ENTRYPOINT ["/bondfetch"]
