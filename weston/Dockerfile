# The weston image: the vulkan image plus the compositor, every library
# it loads, the two diagnostic programs, and liken's two modules for
# it: the hotplug shim and the ivi-shell layout controller.
# display-operator's image builds on it, and its pod runs this
# compositor.
#
# The image builds on vulkan rather than beside it. A node that draws
# with the compositor and with the clients then holds glibc, libdrm and
# LLVM once. Docker shares a layer only when the whole chain under it
# matches, so the compositor goes above the vulkan tree.
#
# The compositor ships in a workload's image and not in the read-only
# root that every liken machine boots, so a machine with no screens
# does not download weston.
#
# Every stage below installs from the same Debian image and the same
# snapshot date as the vulkan image, so each library that the trees
# share is the same file, and the two modules link the glibc that the
# compositor runs on. snapshot.sh and closure.sh come from the vulkan
# directory, the build context named builder.

# The shim needs no libudev to build, because its source declares the
# two libudev types it names.
FROM debian:sid-slim@sha256:fb75970a5ba2a81ca409ed0cabcf05d53e058086fd397f7a63943ded5bd15f84 AS shim
ARG VERSION
COPY --from=builder snapshot.sh snapshot.sha256 /
RUN sh /snapshot.sh "$VERSION" \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*
COPY hotplug/udev-kernel-group.c /
RUN gcc -Wall -Wextra -Werror -shared -fPIC \
        -o /udev-kernel-group.so /udev-kernel-group.c

# The ivi-shell controller module is a separate stage from the shim
# because it needs the compositor's development packages, which the
# shim does not.
#
# Debian installs ivi-layout-export.h in no package, so layout/ carries
# a copy from the weston 16.0.0 tag. A new weston can add members in
# the middle of the interface the header declares, so the copy changes
# with weston. The .so links nothing: weston resolves
# every libweston and libwayland symbol in it at dlopen, which is why
# pkg-config gives only the include paths here.
FROM debian:sid-slim@sha256:fb75970a5ba2a81ca409ed0cabcf05d53e058086fd397f7a63943ded5bd15f84 AS layout
ARG VERSION
COPY --from=builder snapshot.sh snapshot.sha256 /
RUN sh /snapshot.sh "$VERSION" \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        gcc libc6-dev pkg-config \
        libweston-16-dev libwayland-dev libpixman-1-dev \
    && rm -rf /var/lib/apt/lists/*
COPY layout/liken-layout.c layout/ivi-layout-export.h /
RUN gcc -Wall -Wextra -Werror -shared -fPIC \
        $(pkg-config --cflags libweston-16 wayland-server pixman-1) \
        -o /liken-layout.so /liken-layout.c

# weston-closure.sh names weston 16. A snapshot that moves weston to 17
# fails this build, which is the report that the module set and the
# copy of ivi-layout-export.h need reading again.
FROM debian:sid-slim@sha256:fb75970a5ba2a81ca409ed0cabcf05d53e058086fd397f7a63943ded5bd15f84 AS closure
ARG VERSION
COPY --from=builder snapshot.sh snapshot.sha256 /
# libgl1-mesa-dri is what the GL renderer builds its EGL device from.
# Without it weston falls back to the pixman renderer, which advertises
# zwp_linux_dmabuf_v1 at version 3 and leaves mpv on software paths.
# wayland-utils provides wayland-info, which lists every global the
# compositor advertises and is the first thing to read when a client
# connects and draws nothing. kubectl exec runs it by name, which is
# the only way to run anything in an image with no shell.
# ddcutil is the diagnostic for the panels. It reads a panel's whole
# capabilities string and every VCP code over the same i2c node that
# display-operator writes two codes on, so it answers whether the panel
# or the operator is the part that is wrong when a monitor publishes no
# control attribute.
RUN sh /snapshot.sh "$VERSION" \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        weston \
        libgl1-mesa-dri \
        wayland-utils \
        ddcutil \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder closure.sh /
COPY weston-closure.sh /
# The tree of the vulkan image, so the closure can leave out every file
# it already holds.
COPY --from=vulkan / /base/
RUN sh /weston-closure.sh /out /base

FROM vulkan
COPY --from=closure /out /
# display-operator preloads this library into the compositor. It moves
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
