# The final image is scratch with only the static Go binaries, because
# the container has the operator's cluster credentials, and a shell or
# other tools would help an attacker who gets into it.
#
# The Deployment runs /equipment-operator. The cec DaemonSet runs
# /equipment-operator-node, built with the tag node, which leaves out
# client-go's leader election (leader_node.go explains why).

FROM golang AS build
# proxy.golang.org sometimes drops a module download partway through.
# With the default comma separator, `go` falls back to the origin only
# when the proxy answers 404 or 410, so a dropped download fails the
# build. With a pipe, `go` falls back to the origin after any proxy
# error, using the git in this image. `GOSUMDB` keeps its default, so
# the checksum database still verifies modules fetched from the origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
# The module files are copied before the source, so a source edit
# reuses the cached download layer.
COPY go.mod go.sum ./
# go.mod replaces the kubernetes module with ../kubernetes. The build
# receives that directory as the build context kubernetes and copies it
# to /kubernetes, so ../kubernetes resolves from /src.
COPY --from=kubernetes go.mod go.sum /kubernetes/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
COPY *.go ./
# The driver packages are subdirectories, which `COPY *.go` does not
# copy.
COPY denon ./denon
COPY equipment ./equipment
COPY wiim ./wiim
COPY cec ./cec
COPY upnp ./upnp
# -ldflags sets main.version, so the liken_build_info metric reports
# the release that is running instead of dev.
ARG VERSION=dev
# CGO_ENABLED=0 makes a static binary, because scratch has no dynamic
# loader.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /equipment-operator . \
    && CGO_ENABLED=0 go build -trimpath -tags node -ldflags "-s -w -X main.version=${VERSION}" -o /equipment-operator-node .
# Copies the license notices of the Go standard library and of every
# module that either binary links.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices . \
    && CGO_ENABLED=0 GOFLAGS=-tags=node sh /go.sh /notices .

FROM scratch
COPY --from=build /equipment-operator /equipment-operator
COPY --from=build /equipment-operator-node /equipment-operator-node
COPY --from=build /notices /
ENTRYPOINT ["/equipment-operator"]
