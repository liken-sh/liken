# The image of firmware-operator: one static Go binary, its license
# notices, and the CA bundle for HTTPS catalog downloads. The operator
# has cluster credentials and starts pods on every machine, so the
# image has no shell, libc, or tools that an attacker could use. The
# operator verifies the API server with the CA in
# its ServiceAccount's token volume, not with this bundle.

FROM golang AS build
# proxy.golang.org sometimes drops a module download partway through.
# With the default comma, `go` falls back to the origin only when the
# proxy returns 404 or 410, so a dropped download fails the build. With
# a pipe, `go` falls back to the origin after any proxy error, using
# the git in this image. `GOSUMDB` keeps its default, so the checksum
# database still verifies a module that `go` fetched from its origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
# Copying the module files first lets a source edit reuse the cached
# download layer.
COPY go.mod go.sum ./
# go.mod replaces the shared kubernetes module with ../kubernetes, so
# the build context `kubernetes` is copied to /kubernetes, the same path
# relative to /src. Its module files are copied first for the same
# reason.
COPY --from=kubernetes go.mod go.sum /kubernetes/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
COPY *.go ./
# scratch has no dynamic loader, so CGO_ENABLED=0 builds a static
# binary. -trimpath keeps the build machine's paths out of it.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /firmware-operator .
# notices/go.sh copies the license files of the Go standard library
# and of each module that the binary links. go.sh explains why the
# image needs them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

FROM scratch
COPY --from=build /firmware-operator /firmware-operator
COPY --from=build /notices /
# A scratch image has no trust store, so without the bundle that the
# trust component pins, every HTTPS download of a catalog fails.
COPY --from=trust / /
# deploy/operator.yaml runs the operator as this user. The pods that
# read and write firmware run the scan image as root.
USER 65534:65534
ENTRYPOINT ["/firmware-operator"]
