# The operator's image: static Go binaries and nothing else. The
# operator holds the cluster credentials and writes every status, so
# its image carries no shell, no libc, and no tools, which is the
# least there is to attack.
#
# The image holds two builds of the program. /equipment-operator is
# the full build, and the Deployment runs it. /equipment-operator-node
# is the node build, with the build tag node, and the cec DaemonSet
# runs it on each node with a CEC adapter. The node build leaves out
# client-go's leader election, which the Deployment alone needs, so
# the program on each node is about half the size. leader_node.go says
# why.

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
COPY *.go ./
# The driver packages are their own directories, and a plain *.go copy
# leaves them out.
COPY denon ./denon
COPY equipment ./equipment
COPY wiim ./wiim
COPY cec ./cec
COPY upnp ./upnp
# The version reaches the binary through -ldflags, so liken_build_info
# names the release actually running and not dev.
ARG VERSION=dev
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. It runs
# from scratch, where there is no loader to need.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /equipment-operator . \
    && CGO_ENABLED=0 go build -trimpath -tags node -ldflags "-s -w -X main.version=${VERSION}" -o /equipment-operator-node .
# The license files of the Go standard library and of each module that
# the two binaries link. notices/go.sh says why the image needs them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices . \
    && CGO_ENABLED=0 GOFLAGS=-tags=node sh /go.sh /notices .

FROM scratch
COPY --from=build /equipment-operator /equipment-operator
COPY --from=build /equipment-operator-node /equipment-operator-node
COPY --from=build /notices /
ENTRYPOINT ["/equipment-operator"]
