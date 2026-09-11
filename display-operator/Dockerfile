# Six images from one file, each built on the one under it.
#
#   --target vulkan    ghcr.io/liken-sh/vulkan, the Vulkan loader, the
#                      Intel and AMD drivers, and the client libraries
#                      a Wayland program opens, on nothing else. The
#                      media browser and the idle screen build FROM it.
#   --target vaapi     ghcr.io/liken-sh/vaapi, that image plus the
#                      VA-API loader and the Intel media driver, for a
#                      program that decodes video on the node's GPU.
#   --target ffmpeg    ghcr.io/liken-sh/ffmpeg, that image plus ffmpeg
#                      and ffprobe and every library they load. The
#                      library operator's file facts build FROM it.
#   --target mpv       ghcr.io/liken-sh/mpv, the ffmpeg image plus mpv
#                      and every library and data file it opens by
#                      name. The media operator's player builds FROM it.
#   --target weston    ghcr.io/liken-sh/weston, the vulkan image plus
#                      the compositor and every library it loads.
#   the default        ghcr.io/liken-sh/display-operator, the weston
#                      image plus the operator's binary.
#
# Each image is built from the one below it rather than beside it. The
# compositor that the release starts is the same set of bytes that the
# pod runs, and a node that draws with the compositor and the clients
# holds glibc, libdrm and LLVM once. Docker shares a layer only when
# the whole chain under it matches, which is why the order is fixed:
# the base first, each image on its base, the operator last.
#
# The compositor ships in a workload's image and not in the read-only
# root that every liken machine boots. That is why the device operator
# pattern puts the compositor in a separate repository: a machine with
# no screens does not download weston.

FROM golang:1.27.0-bookworm AS build
WORKDIR /src
# The module files come first, so a source edit reuses the cached
# download layer.
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# The version reaches the binary through -ldflags, so a running
# operator's liken_build_info names the release it was built from,
# not the "dev" default main.go carries.
ARG VERSION=dev
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it. The binary
# is the whole of the operator image, so it also has to run with no
# loader and no libc under it.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=${VERSION}" -o /display-operator .

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

# The ivi-shell controller module builds on the same Debian suite for
# the same reason: weston dlopens it, so both link the same glibc. It
# is a separate stage from the shim because it needs the compositor's
# development packages, which the shim does not.
#
# ivi-layout-export.h is not in libweston-14-dev, so layout/ carries a
# copy from the weston 14.0 tag. The .so links nothing: weston resolves
# every libweston and libwayland symbol in it at dlopen, which is why
# pkg-config gives only the include paths here.
FROM debian:trixie-slim AS layout
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        gcc libc6-dev pkg-config \
        libweston-14-dev libwayland-dev libpixman-1-dev \
    && rm -rf /var/lib/apt/lists/*
COPY layout/liken-layout.c layout/ivi-layout-export.h /
RUN gcc -Wall -Wextra -Werror -shared -fPIC \
        $(pkg-config --cflags libweston-14 wayland-server pixman-1) \
        -o /liken-layout.so /liken-layout.c

# The suite is pinned because the closure script names weston 14. A
# Debian that moves weston to 15 fails this build, which is the report
# that the module set needs reading again.
FROM debian:trixie-slim AS closure
# libgl1-mesa-dri is what the GL renderer builds its EGL device from.
# Without it weston falls back to the pixman renderer, which advertises
# zwp_linux_dmabuf_v1 at version 3 and leaves mpv on software paths.
# wayland-utils provides wayland-info, which lists every global the
# compositor advertises and is the first thing to read when a client
# connects and draws nothing. kubectl exec runs it by name, which is
# the only way to run anything in an image with no shell.
# mesa-vulkan-drivers and libvulkan1 are the drivers and the loader of
# the vulkan image. libxkbcommon0 and tzdata carry the keymap data and
# the zoneinfo that its clients read.
# ddcutil is the diagnostic for the panels. It reads a panel's whole
# capabilities string and every VCP code over the same i2c node this
# operator writes two codes on, so it answers whether the panel or
# the operator is the part that is wrong when a monitor publishes no
# control attribute. It lands in both images because one closure
# builds one tree, and separating it would cost the operator image a
# second copy of the mesa libraries for 1.5 MB saved on the other.
# libva2, libva-drm2 and intel-media-va-driver are the VA-API tree, and
# ffmpeg is the two programs of the image above it. They install in the
# same builder as weston, and the weston tree is unchanged by them,
# checked layer by layer when they were added.
# mpv is the one program of the image above ffmpeg. libspa-0.2-modules
# carries the SPA plugins its PipeWire output loads by name, pipewire-bin
# is installed for the one file /usr/share/pipewire/client.conf, and
# fontconfig-config carries the configuration libass reads. The other
# trees are unchanged by them, checked layer by layer when they were
# added.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        weston \
        libgl1-mesa-dri \
        wayland-utils \
        ddcutil \
        mesa-vulkan-drivers \
        libvulkan1 \
        libxkbcommon0 \
        tzdata \
        libva2 \
        libva-drm2 \
        intel-media-va-driver \
        ffmpeg \
        mpv \
        libpipewire-0.3-0t64 \
        libspa-0.2-modules \
        pipewire-bin \
        fontconfig-config \
    && rm -rf /var/lib/apt/lists/*
COPY closure.sh vulkan-closure.sh vaapi-closure.sh ffmpeg-closure.sh mpv-closure.sh weston-closure.sh /
# Each tree after the first is computed whole, then less every file the
# trees under its image already hold, so each layer carries only what
# its base lacks.
RUN sh /vulkan-closure.sh /out/vulkan \
    && sh /vaapi-closure.sh /out/vaapi /out/vulkan \
    && sh /ffmpeg-closure.sh /out/ffmpeg /out/vaapi /out/vulkan \
    && sh /mpv-closure.sh /out/mpv /out/ffmpeg /out/vaapi /out/vulkan \
    && sh /weston-closure.sh /out/weston /out/vulkan

FROM scratch AS vulkan
COPY --from=closure /out/vulkan /

# The VA-API loader and driver, for a program that decodes on the node's GPU.
FROM vulkan AS vaapi
COPY --from=closure /out/vaapi /

# ffmpeg and ffprobe on the VA-API image. The entrypoint is ffmpeg, so a
# release can run it and read what it says.
FROM vaapi AS ffmpeg
COPY --from=closure /out/ffmpeg /
ENTRYPOINT ["/usr/bin/ffmpeg"]

# mpv on the ffmpeg image, which already holds the libav libraries mpv
# links and the VA-API driver it decodes with. The entrypoint is mpv, so
# a release can run it and read what it says.
FROM ffmpeg AS mpv
COPY --from=closure /out/mpv /
ENTRYPOINT ["/usr/bin/mpv"]

FROM vulkan AS weston
COPY --from=closure /out/weston /
# The operator preloads this library into the compositor. It moves
# the compositor's hotplug subscription onto the kernel's netlink
# group. The loader opens the library by absolute path, so it needs no
# entry in the cache that the closure built.
COPY --from=shim /udev-kernel-group.so /usr/lib/liken/udev-kernel-group.so
# weston loads the controller module from the modules= list in
# weston.ini, and it searches this directory for the name it is given.
COPY --from=layout /liken-layout.so /usr/lib/x86_64-linux-gnu/weston/liken-layout.so
# The image runs the compositor and holds no other program, so a
# release can start it and read what it says.
ENTRYPOINT ["/usr/bin/weston"]

FROM weston
# The operator's binary is the entrypoint of all three of the pod's
# containers. The argument the manifest passes selects the role: the
# config write, the compositor it execs, or, with no argument, the
# DRA driver.
COPY --from=build /display-operator /usr/local/bin/display-operator

ENTRYPOINT ["/usr/local/bin/display-operator"]
