# The bondfetch image: one static binary on an empty filesystem.
#
# bondfetch is the first init container of the operator's pod. It
# reads the adapter's bonds and identity key from their Secrets and
# the settings of its Adapter, writes them into the volumes that
# bluetoothd reads, and exits. bluetoothd reads the bonds and the key
# only when it starts, so bondfetch has to finish first.
#
# bondfetch reads the adapter's address from an HCI socket, connects
# to the API server at KUBERNETES_SERVICE_HOST and verifies its
# certificate against the CA that the kubelet mounts, and writes files
# into mounted directories. So the image needs no certificate store,
# no resolver configuration, and no zoneinfo.

FROM golang AS build
# proxy.golang.org sometimes drops a module download partway through.
# With the default comma separator, `go` falls back to the module's
# origin only when the proxy answers 404 or 410, so a dropped download
# fails the build. With a pipe, `go` falls back after any proxy error,
# using the git in the `golang` image. `GOSUMDB` keeps its default, so
# the checksum database still verifies modules fetched from the origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
COPY go.mod go.sum ./
# go.mod replaces the shared kubernetes module with ../kubernetes. The
# build context named kubernetes is copied to /kubernetes, so that
# path resolves from /src. bondfetch imports its apiclient package, so
# the build needs the whole module and not only its go.mod.
COPY --from=kubernetes go.mod go.sum /kubernetes/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
COPY bonds/ ./bonds/
COPY bondfetch/ ./bondfetch/
# -s -w remove the symbol table and the DWARF sections. A panic still
# prints function names, because Go reads them from the pclntab, which
# -s -w keeps.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /bondfetch ./bondfetch
# Collects the license files of the Go standard library and of each
# linked module. notices/go.sh explains why the image needs them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices ./bondfetch

FROM scratch
COPY --from=build /bondfetch /bondfetch
COPY --from=build /notices /
ENTRYPOINT ["/bondfetch"]
