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

FROM golang:1.26.5-bookworm AS build
WORKDIR /src
# The module files come first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# The bond storage the operator shares with bondfetch. A wildcard over
# the root does not reach a directory, so this package is named.
COPY bonds/ ./bonds/
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. -s -w drop
# the symbol table and the DWARF sections, which is a quarter of the
# binary and costs nothing a reader of this program uses: Go reads
# panic traces from its own pclntab, which stays.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /bluetooth-operator .

FROM scratch
COPY --from=build /bluetooth-operator /bluetooth-operator
ENTRYPOINT ["/bluetooth-operator"]
