# The operator's image: one static Go binary and nothing else. The
# operator holds the credentials that create pods in its namespace, so
# the image carries no shell, no libc, and no tools, which is the least
# there is to attack. It speaks TLS only to the API server, with the CA
# that the kubelet mounts, so it needs no trust store.

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
# The operator's package and the four packages it imports: the INDI
# client, the PHD2 client, the resources' types, and the map of images.
COPY *.go ./
COPY indi/*.go ./indi/
COPY phd2/*.go ./phd2/
COPY observatory/*.go ./observatory/
COPY drivers/*.go ./drivers/
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. It runs
# from scratch, where there is no loader to need.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /observatory-operator .
# The license files of the Go standard library and of each module that
# the binary links. notices/go.sh says why the image needs them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

FROM scratch
COPY --from=build /observatory-operator /observatory-operator
COPY --from=build /notices /
ENTRYPOINT ["/observatory-operator"]
