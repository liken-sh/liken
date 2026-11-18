# The image holds one static Go binary, its license notices, and a CA
# bundle, with no shell, no libc, and no tools. The binary decodes
# pictures from outside the cluster, in the operator, which has the
# cluster credentials, and in the baker pods. A flaw in a decoder
# therefore gives an attacker no programs to run.

FROM golang AS build
# proxy.golang.org sometimes drops a module download partway through.
# With the default comma separator, `go` falls back to the origin only
# after a 404 or a 410, so a dropped download fails the build. With a
# pipe, `go` falls back after any proxy error. `GOSUMDB` keeps its
# default, so the checksum database still verifies those modules.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
# The module files are copied first, so a source edit reuses the
# cached download layer.
COPY go.mod go.sum ./
# go.mod replaces the kubernetes module with ../kubernetes, so the
# build takes that directory as the build context kubernetes and copies
# it to /kubernetes, next to /src.
COPY --from=kubernetes go.mod go.sum /kubernetes/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
COPY *.go ./
# CGO_ENABLED=0 builds a static binary, which runs on scratch without a
# dynamic loader.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /people-operator .
# notices/go.sh copies the license files of the Go standard library
# and of each module that the binary links.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

FROM scratch
COPY --from=build /people-operator /people-operator
COPY --from=build /notices /
# scratch has no CA certificates, so without this bundle every HTTPS
# picture fetch fails. The trust component pins one bundle for every
# `liken` image.
COPY --from=trust / /
ENTRYPOINT ["/people-operator"]
