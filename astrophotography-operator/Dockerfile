# The operator's image: one static Go binary and nothing else. The
# operator can create Jobs in every namespace it serves, so the image
# has no shell and no tools for an attacker to use. Its only TLS peer
# is the API server, whose CA the kubelet mounts, so the image needs no
# trust store. The promotion Jobs run this image in promote mode.

FROM golang AS build
# proxy.golang.org sometimes drops a module download partway through.
# With the default comma, `go` falls back to the origin only after a
# 404 or 410, so a dropped download fails the build. With a pipe, it
# falls back after any proxy error. The checksum database still
# verifies a module fetched from the origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
# Copying the module files first lets a source edit reuse the cached
# download layer.
COPY go.mod go.sum ./
# go.mod replaces the `kubernetes` module with ../kubernetes and
# observatory-operator with ../observatory-operator. The build contexts
# `kubernetes` and `observatory` supply those directories, copied to
# the same relative paths from /src.
COPY --from=kubernetes go.mod go.sum /kubernetes/
COPY --from=observatory go.mod go.sum /observatory-operator/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
# The three packages of observatory-operator that this operator
# imports: the INDI client, the PHD2 client, and the resource types.
COPY --from=observatory indi/*.go /observatory-operator/indi/
COPY --from=observatory phd2/*.go /observatory-operator/phd2/
COPY --from=observatory observatory/*.go /observatory-operator/observatory/
# The operator's own package and the component's other packages.
COPY *.go ./
COPY astrophotography/*.go ./astrophotography/
COPY catalog/*.go ./catalog/
COPY fits/*.go ./fits/
COPY outbox/*.go ./outbox/
COPY promote/*.go ./promote/
COPY sky/*.go ./sky/
COPY solver/*.go ./solver/
COPY worker/*.go ./worker/
# The version goes into the binary, so the `liken_build_info` metric
# reports the release and not `dev`.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /astrophotography-operator .
# The license files of the Go standard library and of each module the
# binary links. notices/go.sh explains why the image carries them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

FROM scratch
COPY --from=build /astrophotography-operator /astrophotography-operator
COPY --from=build /notices /
# deploy/operator.yaml runs the operator as this user, and `docker run`
# does too. The promotion Jobs set their own user.
USER 65534:65534
ENTRYPOINT ["/astrophotography-operator"]
