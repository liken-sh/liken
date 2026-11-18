# The operator's image: one static Go binary and nothing else. The
# operator's service account can create pods, so the image has no shell,
# no libc, and no tools that an attacker could use. The operator uses
# TLS only to reach the API server, and it verifies that connection
# with the CA file that the kubelet mounts, so the image needs no CA
# bundle.

FROM golang AS build
# proxy.golang.org sometimes drops a module download partway through.
# After a comma, `go` tries the origin only when the proxy returns 404
# or 410. After a pipe, it tries the origin after any proxy error. The
# default `GOSUMDB` still verifies a module fetched from its origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
# The module files come first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
# go.mod replaces the kubernetes module with ../kubernetes, so the
# build context named kubernetes is copied to /kubernetes. Its go.mod
# and go.sum come first, for the same caching reason.
COPY --from=kubernetes go.mod go.sum /kubernetes/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
# The operator's package and the packages it imports: the INDI client,
# the PHD2 client, the resource types, and the map of driver images.
COPY *.go ./
COPY indi/*.go ./indi/
COPY phd2/*.go ./phd2/
COPY observatory/*.go ./observatory/
COPY drivers/*.go ./drivers/
# The build writes the version into the binary through -ldflags, so the
# liken_build_info metric reports the release version and not dev.
ARG VERSION=dev
# CGO_ENABLED=0 builds a static binary, which runs on scratch.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /observatory-operator .
# The license files of the Go standard library and of each module the
# binary links. notices/go.sh explains why the image carries them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

FROM scratch
COPY --from=build /observatory-operator /observatory-operator
COPY --from=build /notices /
# deploy/operator.yaml runs the operator as this user. The image sets
# the same user, so `docker run` behaves the same way.
USER 65534:65534
ENTRYPOINT ["/observatory-operator"]
