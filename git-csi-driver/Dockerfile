# The image is a closure on scratch. The driver runs git and ssh and
# reads one CA bundle, and git-closure.sh collects those and the files
# they open into a tree the final stage copies whole. What the driver
# never runs is not in the image.

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
RUN go mod download
COPY *.go ./
# The version reaches the binary through -ldflags, so a running driver
# names the release it was built from.
ARG VERSION=dev
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it.
#
# -s -w drops the symbol table and the DWARF data, which are 20 MB
# of the binary. The driver reports its version through --version and
# its log, and a panic's stack trace keeps its function names without
# the symbol table, so nothing this project reads is lost.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /git-csi-driver .

FROM debian:trixie-slim AS closure
# git is the program the driver runs. openssh-client is the transport a
# deploy key uses. ca-certificates verifies an HTTPS forge.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        git \
        openssh-client \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY git-closure.sh /
RUN sh /git-closure.sh /out

FROM scratch
COPY --from=closure /out /

COPY --from=build /git-csi-driver /usr/local/bin/git-csi-driver

ENTRYPOINT ["/usr/local/bin/git-csi-driver"]
