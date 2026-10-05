# The operator's image: one static binary on an empty filesystem.
#
# Nothing else belongs here. The operator reaches the API server at
# the address KUBERNETES_SERVICE_HOST names, over TLS it verifies
# against the CA the kubelet mounts, so the image needs no certificate
# store and no resolver configuration. It writes CDI files and serves
# a socket to the kubelet, both under mounted directories. It formats
# no time in a named zone, so it needs no zoneinfo. Everything the
# daemon side needs is in the bluetoothd image, which the same pod
# runs beside this one.

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
# The bond storage the operator shares with bondfetch. A wildcard over
# the root does not reach a directory, so this package is named.
COPY bonds/ ./bonds/
# VERSION names the release this binary is, for liken_build_info. A
# build with no argument reports "dev", which is every local build and
# every test run: nothing here reads its own version to decide what to
# do, so the default costs nothing.
ARG VERSION=dev
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. -s -w drop
# the symbol table and the DWARF sections, which is a quarter of the
# binary and costs nothing a reader of this program uses: Go reads
# panic traces from its own pclntab, which stays.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /bluetooth-operator .
# The license files of the Go standard library and of each module that
# the binary links. notices/go.sh says why the image needs them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

FROM scratch
COPY --from=build /bluetooth-operator /bluetooth-operator
COPY --from=build /notices /
ENTRYPOINT ["/bluetooth-operator"]
