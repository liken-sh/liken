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
#
# weston itself builds from the upstream release, because a fix that
# liken needs reaches a weston point release before it reaches sid.
# weston 16.0.0's DRM backend wrote a transparent CRTC background
# colour whenever it could not lower a solid view, and i915 refuses
# every atomic commit that carries one, so the screen froze at the
# start of a film. 16.0.1 fixed it. Every library weston builds against
# still comes from the snapshot.

# The release tarball, pinned by its sha256. BuildKit checks the
# checksum when it fetches the file, so no CA bundle enters the build.
# The gitlab release of the tag publishes the tarball, its sha256sum
# file, and a signature by the release manager; latest.sh reads that
# release and writes the URL and the checksum here.
ARG WESTON_VERSION=16.0.2
ARG WESTON_URL=https://gitlab.freedesktop.org/-/project/120/uploads/7158a97a42a6d8562081e84b49fc8c3a/weston-16.0.2.tar.xz
ARG WESTON_SHA256=49dd87d18cb229384264d2f69cb5a51f14f61a111e9227bfedb91b3ca8c39643

# The shim needs no libudev to build, because its source declares the
# two libudev types it names.
FROM debian AS shim
ARG VERSION
COPY --from=builder snapshot.sh snapshot.sha256 /
RUN sh /snapshot.sh "$VERSION" \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*
COPY hotplug/udev-kernel-group.c /
RUN gcc -Wall -Wextra -Werror -shared -fPIC \
        -o /udev-kernel-group.so /udev-kernel-group.c

# weston, built with meson into /usr, which the layout stage builds
# against, and into /stage, which the closure takes.
#
# The build turns on what the closure ships and nothing else: the DRM
# and headless backends, the GL renderer, and the ivi-shell. The other
# backends, the Vulkan renderer, Xwayland, the other shells, the lcms
# colour manager, the systemd notifier and the image loaders would
# only pull libraries that no liken screen loads. weston.ini enables no
# colour management, so the lcms module was never loaded. The ivi-shell
# option also builds weston-screenshooter, which the layout smoke reads
# the screen with, and the shm client is the window that smoke places.
#
# dpkg-buildflags gives the same hardening flags as Debian's build of
# the same release, and the install strips the binaries as Debian's
# packages are stripped.
FROM debian AS build
ARG VERSION
ARG WESTON_VERSION
ARG WESTON_SHA256
ARG WESTON_URL
COPY --from=builder snapshot.sh snapshot.sha256 /
RUN sh /snapshot.sh "$VERSION" \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        gcc g++ libc6-dev pkg-config meson ninja-build dpkg-dev xz-utils \
        libwayland-dev libwayland-bin wayland-protocols \
        libpixman-1-dev libxkbcommon-dev libinput-dev libevdev-dev \
        libdrm-dev libudev-dev libseat-dev libdisplay-info-dev \
        libgbm-dev libegl-dev libgles-dev libcairo2-dev \
    && rm -rf /var/lib/apt/lists/*
ADD --checksum=sha256:${WESTON_SHA256} ${WESTON_URL} /weston.tar.xz
RUN mkdir /src && tar -C /src --strip-components=1 -xf /weston.tar.xz \
    && cd /src \
    && export DEB_BUILD_MAINT_OPTIONS=hardening=+all \
    && CFLAGS="$(dpkg-buildflags --get CFLAGS) $(dpkg-buildflags --get CPPFLAGS)" \
       LDFLAGS="$(dpkg-buildflags --get LDFLAGS)" \
       meson setup build \
        --prefix=/usr --libdir=lib/x86_64-linux-gnu --libexecdir=lib/x86_64-linux-gnu \
        --buildtype=plain --wrap-mode=nodownload \
        -Dbackend-drm=true -Dbackend-headless=true \
        -Dbackend-pipewire=false -Dbackend-rdp=false -Dbackend-vnc=false \
        -Dbackend-wayland=false -Dbackend-x11=false \
        -Drenderer-gl=true -Drenderer-vulkan=false \
        -Dxwayland=false -Dsystemd=false \
        -Dshell-ivi=true -Dshell-desktop=false -Dshell-kiosk=false -Dshell-lua=false \
        -Dcolor-management-lcms=false -Dimage-jpeg=false -Dimage-webp=false \
        -Dtools=[] -Ddemo-clients=false -Dsimple-clients=shm \
        -Dtests=false -Ddoc=false -Dperfetto=false \
    && meson compile -C build \
    && meson install -C build --strip --destdir /stage \
    && install -D -m 644 COPYING /stage/usr/share/doc/weston/COPYING \
    && cp -a /stage/. / \
    && test "$(/usr/bin/weston --version)" = "weston ${WESTON_VERSION}"

# The ivi-shell controller module builds against the libweston of the
# stage above. layout/build.sh builds the module and liken-arrange-drill,
# the client that drives liken_arrangement_v1 by hand, and its comment says
# how.
#
# libweston installs no ivi-layout-export.h, so layout/ carries a copy
# from the weston tag. A new weston can add members in the middle of
# the interface the header declares, so the copy changes with weston.
FROM build AS layout
COPY layout/ /layout/
RUN sh /layout/build.sh /out

# weston-closure.sh names weston 16. A release that moves weston to 17
# fails this build, which is the report that the module set and the
# copy of ivi-layout-export.h need reading again. The closure starts
# from the build stage, so every library that weston links is already
# installed, and it adds the packages of the programs and drivers it
# ships beside weston.
FROM build AS closure
ARG VERSION
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
    && apt-get install -y --no-install-recommends \
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
COPY --from=layout /out/liken-layout.so /usr/lib/x86_64-linux-gnu/weston/liken-layout.so
# A person runs the drill with kubectl exec in the compositor's pod,
# where every claim's socket is under XDG_RUNTIME_DIR. It loads
# libwayland-client, which the closure holds for wayland-info.
COPY --from=layout /out/liken-arrange-drill /usr/bin/liken-arrange-drill
# The image runs the compositor and holds no other program, so a
# release can start it and read what it says.
ENTRYPOINT ["/usr/bin/weston"]
