# The operator reaches the API server by KUBERNETES_SERVICE_HOST and
# verifies it against the CA that the kubelet mounts, so the image needs
# no certificate store and no resolver configuration. Its CDI files and
# its kubelet socket are in mounted directories.

FROM golang AS build
# proxy.golang.org sometimes drops a module download partway through.
# With the default comma, `go` falls back to the origin only when the
# proxy responds 404 or 410, so a dropped download fails the build.
# With a pipe, `go` falls back after any proxy error. The checksum
# database still verifies a module fetched from its origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
# Copy the module files first so that a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
# go.mod replaces the kubernetes module with ../kubernetes. The build
# context kubernetes is copied to /kubernetes, which is ../kubernetes
# from /src.
COPY --from=kubernetes go.mod go.sum /kubernetes/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
COPY *.go ./
# `COPY *.go` copies no directories, so the bonds package, which
# bondfetch shares, is copied by name.
COPY bonds/ ./bonds/
# VERSION is the release that liken_build_info reports. A build without
# the argument reports "dev".
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /bluetooth-operator .
# The license files of the Go standard library and of each module that
# the binary links. See notices/go.sh for why the image needs them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

FROM scratch
COPY --from=build /bluetooth-operator /bluetooth-operator
COPY --from=build /notices /
ENTRYPOINT ["/bluetooth-operator"]
