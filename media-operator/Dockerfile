# The operator's image: static Go binaries on scratch, with no shell,
# libc, or tools. The operator holds the cluster credentials, so the
# image has nothing in it to attack beyond the binaries.
#
# The component's other images build from the Dockerfiles beside this
# one, and package.toml lists them. The sidecar image is this image under
# a second name: package.toml lists it as an alias, and no Dockerfile
# builds it. A release ships every image of the component together.
#
# The image holds two builds of the program. /media-operator is the full
# build, which the operator runs. /media-operator-pod is built with the
# `pod` build tag, and every other role runs it, including the sidecar in
# each playback pod and in each Remote's pod. It leaves out client-go's
# leader election, which only the operator needs (leader_pod.go), so the
# copy in every pod is smaller and uses less memory at start. Because the
# sidecar image is this image, each node that runs a sidecar pulls both
# binaries.

FROM golang AS build
# With the default comma, `go` falls back to the module's origin only
# when the proxy returns 404 or 410, so a download that proxy.golang.org
# drops partway fails the build. With a pipe, `go` falls back to the
# origin after any proxy error, using the git in this image. GOSUMDB
# keeps its default, so the checksum database still verifies modules
# fetched from the origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
# Module files first, so a source edit reuses the cached download layer.
COPY go.mod go.sum ./
# go.mod replaces the kubernetes module with ../kubernetes, so the
# `kubernetes` build context is copied to /kubernetes, the same path
# relative to /src. Its module files come first for the same reason.
COPY --from=kubernetes go.mod go.sum /kubernetes/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
COPY *.go ./
# The api role embeds the OpenAPI document at compile time.
COPY openapi.json ./
# *.go does not copy subdirectories, and the binary imports edl.
COPY edl/ ./edl/
# scratch has no loader, so CGO_ENABLED=0 builds static binaries.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /media-operator . \
    && CGO_ENABLED=0 go build -trimpath -tags pod -ldflags "-s -w" -o /media-operator-pod .
# License files of the Go standard library and of each module that either
# binary links. notices/go.sh says why the image needs them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices . \
    && CGO_ENABLED=0 GOFLAGS=-tags=pod sh /go.sh /notices .

FROM scratch
COPY --from=build /media-operator /media-operator
COPY --from=build /media-operator-pod /media-operator-pod
COPY --from=build /notices /
ENTRYPOINT ["/media-operator"]
