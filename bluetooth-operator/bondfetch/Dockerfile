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

FROM golang:1.26.5-bookworm AS build
WORKDIR /src
# The module files come first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
RUN go mod download
COPY bonds/ ./bonds/
COPY bondfetch/ ./bondfetch/
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. -s -w drop
# the symbol table and the DWARF sections, and Go reads panic traces
# from its own pclntab, which stays.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /bondfetch ./bondfetch

FROM scratch
COPY --from=build /bondfetch /bondfetch
ENTRYPOINT ["/bondfetch"]
