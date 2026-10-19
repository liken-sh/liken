# The operator's image: one static Go binary and nothing else. The
# operator holds the credentials that create pods and Jobs in every
# namespace it serves, so the image carries no shell, no libc, and no
# tools, which is the least there is to attack. It speaks TLS only to
# the API server, with the CA that the kubelet mounts, so it needs no
# trust store. The promotion Jobs run the same binary in its promote
# mode.

FROM golang:1.27.2-bookworm AS build
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
# go.mod replaces the shared kubernetes module with ../kubernetes and
# observatory-operator with ../observatory-operator, and the build takes
# those directories as the build contexts named kubernetes and
# observatory. Each lands at the same place relative to /src that its
# directory has relative to this component, and its module files come
# first for the same reason as this module's.
COPY --from=kubernetes go.mod go.sum /kubernetes/
COPY --from=observatory go.mod go.sum /observatory-operator/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
# The three packages of observatory-operator that this operator
# imports: the INDI client, the PHD2 client, and the resources' types.
# Nothing else of that component reaches the build.
COPY --from=observatory indi/*.go /observatory-operator/indi/
COPY --from=observatory phd2/*.go /observatory-operator/phd2/
COPY --from=observatory observatory/*.go /observatory-operator/observatory/
# The operator's package and every package of the component.
COPY *.go ./
COPY astrophotography/*.go ./astrophotography/
COPY catalog/*.go ./catalog/
COPY fits/*.go ./fits/
COPY outbox/*.go ./outbox/
COPY promote/*.go ./promote/
COPY sky/*.go ./sky/
COPY solver/*.go ./solver/
COPY worker/*.go ./worker/
# The version reaches the binary through -ldflags, so liken_build_info
# names the release that runs and not dev.
ARG VERSION=dev
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. It runs
# from scratch, where there is no loader to need.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /astrophotography-operator .
# The license files of the Go standard library and of each module that
# the binary links. notices/go.sh says why the image needs them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

FROM scratch
COPY --from=build /astrophotography-operator /astrophotography-operator
COPY --from=build /notices /
ENTRYPOINT ["/astrophotography-operator"]
