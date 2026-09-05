# The driver runs the git binary, so the final stage is Debian with git
# installed. A closure on scratch, the way audio-operator ships its
# daemons, is a later plan.

FROM golang:1.27.0-bookworm AS build
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
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=${VERSION}" -o /git-csi-driver .

FROM debian:trixie-slim
# git is the program the driver runs. openssh-client is the transport a
# deploy key uses. ca-certificates verifies an HTTPS forge.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        git \
        openssh-client \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /git-csi-driver /usr/local/bin/git-csi-driver

ENTRYPOINT ["/usr/local/bin/git-csi-driver"]
