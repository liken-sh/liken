# Two images from one file.
#
#   --target weston    ghcr.io/liken-sh/weston, the compositor and
#                      every library it loads, on nothing else.
#   the default        ghcr.io/liken-sh/display-operator, that image
#                      plus the operator's binary.
#
# The operator image is built from the weston image rather than beside
# it, so the compositor that the release starts is the same set of
# bytes that the pod runs. The two share every layer, so a node that
# pulls both pulls the second one for the size of one binary.
#
# The compositor ships in a workload's image and not in the read-only
# root that every liken machine boots. That is the device operator
# pattern's whole reason for a separate repository: a machine with no
# screens does not carry weston.

FROM golang:1.26.5-bookworm AS build
WORKDIR /src
# The module files come first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. The binary
# is the whole of the operator image, so it also has to run with no
# loader and no libc under it.
RUN CGO_ENABLED=0 go build -trimpath -o /display-operator .

# The shim builds on the same Debian suite as the compositor, so both
# link the same glibc. The build needs no libudev, because the shim's
# source declares the two libudev types it names.
FROM debian:trixie-slim AS shim
RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*
COPY hotplug/udev-kernel-group.c /
RUN gcc -Wall -Wextra -Werror -shared -fPIC \
        -o /udev-kernel-group.so /udev-kernel-group.c

# The suite is pinned because the closure script names weston 14. A
# Debian that moves weston to 15 fails this build, which is the report
# that the module set needs reading again.
FROM debian:trixie-slim AS closure
# libgl1-mesa-dri is what the GL renderer builds its EGL device from.
# Without it weston falls back to the pixman renderer, which advertises
# zwp_linux_dmabuf_v1 at version 3 and leaves mpv on software paths.
# wayland-utils carries wayland-info, which lists every global the
# compositor advertises and is the first thing to read when a client
# connects and draws nothing. kubectl exec runs it by name, which is
# the only way to run anything in an image with no shell.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        weston \
        libgl1-mesa-dri \
        wayland-utils \
    && rm -rf /var/lib/apt/lists/*
COPY weston-closure.sh /
RUN sh /weston-closure.sh /out

FROM scratch AS weston
COPY --from=closure /out /
# The operator preloads this library into the compositor. It moves
# the compositor's hotplug subscription onto the kernel's netlink
# group. The loader opens the library by absolute path, so it needs no
# entry in the cache that the closure built.
COPY --from=shim /udev-kernel-group.so /usr/lib/liken/udev-kernel-group.so
# The image runs the compositor and holds no other program, so a
# release can start it and read what it says.
ENTRYPOINT ["/usr/bin/weston"]

FROM weston
# The operator is the container's only entrypoint. It writes
# weston.ini from the outputs it enumerates, so it has to run before
# the compositor does, and it starts the compositor itself. The
# container ends when either of them ends.
COPY --from=build /display-operator /usr/local/bin/display-operator

ENTRYPOINT ["/usr/local/bin/display-operator"]
