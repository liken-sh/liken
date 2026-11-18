FROM golang AS build
# proxy.golang.org sometimes drops a module download partway through.
# With a comma, `go` falls back to the origin only when the proxy
# returns 404 or 410, so a dropped download fails the build. With a
# pipe, `go` falls back to the origin after any proxy error. The
# checksum database still verifies a module fetched from its origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
# The module files are copied first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
# go.mod replaces the kubernetes module with ../kubernetes, so the
# build context kubernetes is copied to /kubernetes, which is
# ../kubernetes from /src.
COPY --from=kubernetes go.mod go.sum /kubernetes/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
COPY *.go ./
ARG VERSION=dev
# The flags are the ones every Go image of liken uses: a static binary
# with no paths of the build machine, no symbol table, and no DWARF
# data. The driver reports VERSION through --version and in
# GetPluginInfo.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /per-node-csi-driver .
# The license notices of the Go standard library and of each module
# that the binary links.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

FROM scratch
COPY --from=build /per-node-csi-driver /per-node-csi-driver
COPY --from=build /notices /

ENTRYPOINT ["/per-node-csi-driver"]
