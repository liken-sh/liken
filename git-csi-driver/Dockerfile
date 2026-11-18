# The final image is built from scratch. The driver runs git and ssh and
# reads a CA bundle, so git-closure.sh copies those programs and the
# files they load into one tree, and the last stage copies that tree.
# Nothing else from Debian is in the image.

FROM golang AS build
# proxy.golang.org sometimes drops a module download partway through.
# With the default comma, `go` falls back to the origin only when the
# proxy returns 404 or 410, so a dropped download fails the build. With
# a pipe, `go` falls back after any proxy error, using the git in this
# builder image. `GOSUMDB` keeps its default, so the checksum database
# still verifies a module fetched from its origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
COPY go.mod go.sum ./
# go.mod replaces the liken kubernetes module with ../kubernetes. The
# build receives that directory as a second build context named
# kubernetes and copies it to /kubernetes, so ../kubernetes from /src
# resolves as it does in the repository.
COPY --from=kubernetes go.mod go.sum /kubernetes/
RUN go mod download
COPY --from=kubernetes . /kubernetes/
COPY *.go ./
# -ldflags below sets main.version, so the driver can report the
# release it was built from.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /git-csi-driver .
# notices/go.sh collects the license notices of the Go standard library
# and of each module the binary links, and explains why the image needs
# them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices .

# This stage installs from the same Debian image and the same snapshot
# date as the base images. snapshot.sh and closure.sh come from the
# `vulkan` directory, passed as the build context named builder, and
# SNAPSHOT is vulkan's version.
FROM debian AS closure
ARG SNAPSHOT
COPY --from=builder snapshot.sh snapshot.sha256 closure.sh /
# The driver runs git. openssh-client is the transport for a deploy key.
RUN sh /snapshot.sh "$SNAPSHOT" \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        git \
        openssh-client \
    && rm -rf /var/lib/apt/lists/*
# git-closure.sh sources closure.sh. Besides copying the files, it
# writes the copyright file of each Debian package it took files from,
# the list of those packages and their sources, and a dpkg record for
# each package, which syft and Trivy read.
COPY git-closure.sh /
RUN sh /git-closure.sh /out

FROM scratch
COPY --from=closure /out /
# The CA bundle that verifies an HTTPS forge comes from the `trust`
# component, which pins one bundle for every liken image, with its
# notice.
COPY --from=trust / /

COPY --from=build /git-csi-driver /git-csi-driver
COPY --from=build /notices /

ENTRYPOINT ["/git-csi-driver"]
