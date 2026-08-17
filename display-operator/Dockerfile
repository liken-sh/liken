# The image carries the operator and the compositor it runs. That
# pairing is the device operator pattern's whole reason for a separate
# repository: Weston ships here, in a workload's image, and not in the
# read-only root that every liken machine boots.

FROM golang:1.26.5-bookworm AS build
WORKDIR /src
# The module files come first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it.
RUN CGO_ENABLED=0 go build -trimpath -o /display-operator .

FROM debian:stable-slim
# weston brings the compositor and the kiosk shell. libgl1-mesa-dri is
# what the GL renderer builds its EGL device from, and without it
# weston falls back to the pixman renderer, which advertises
# zwp_linux_dmabuf_v1 at version 3 and leaves mpv on software paths.
# wayland-utils carries wayland-info, which lists every global the
# compositor advertises and is the first thing to read when a client
# connects and draws nothing.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        weston \
        libgl1-mesa-dri \
        wayland-utils \
    && rm -rf /var/lib/apt/lists/*

# The operator is the container's only entrypoint. It writes
# weston.ini from the outputs it enumerates, so it has to run before
# the compositor does, and it starts the compositor itself. The
# container ends when either of them ends.
COPY --from=build /display-operator /usr/local/bin/display-operator

ENTRYPOINT ["/usr/local/bin/display-operator"]
