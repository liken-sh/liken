# The image is the binary alone on scratch. The driver runs no other
# program, so nothing else is in it.

FROM golang:1.27.0-bookworm AS build
WORKDIR /src
# The module files come first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# The version reaches the binary through -ldflags, so a running driver
# names the release it was built from.
ARG VERSION=dev
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it.
#
# -s -w drops the symbol table and the DWARF data, which are 20 MB
# of the binary. The driver reports its version through --version and
# its log, and a panic's stack trace keeps its function names without
# the symbol table, so nothing this project reads is lost.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /per-node-csi-driver .

FROM scratch
COPY --from=build /per-node-csi-driver /usr/local/bin/per-node-csi-driver

ENTRYPOINT ["/usr/local/bin/per-node-csi-driver"]
