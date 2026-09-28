# The bluetoothd image: the three static programs the pod runs
# (bluetoothd, dbus-daemon, and the Go entrypoint), four more that a
# person runs with kubectl exec (bluetoothctl, btmgmt, btmon, and
# dbus-send), their configuration, and nothing else.
#
# bluetoothd ships here, in a workload's image, and not in the
# read-only root that every liken machine boots. That is why the
# device operator pattern puts the daemon in a separate repository, and
# it is why this image builds BlueZ rather than installing a
# distribution's package: a package brings a package manager, a shell,
# and a base filesystem, and this container runs three programs.
#
# The BlueZ shells have one limit in this image. BlueZ parses each
# command's argument specification with wordexp(3), musl implements
# wordexp by running /bin/sh, and this image has none, so the call
# returns WRDE_SYNTAX and bluetoothctl and btmgmt refuse every
# command that takes an argument ("Unable to parse mandatory
# command arguments"). A command whose specification is empty skips
# the call and works: bluetoothctl list, and btmgmt info, extinfo,
# con, keys, and ltks. dbus-send parses with getopt and calls no
# shell, so it reaches every org.bluez method and property the two
# shells cannot.
#
# The binaries are statically linked against musl, so the image is the
# programs and their configuration with no loader and no shared
# objects under them. A distribution's bluetoothd loads six shared
# libraries on Alpine and fifteen on Debian, and each one is a file
# whose version has to agree with the binary that opens it. The static
# binaries here open none, so no version can disagree.
#
# Two things make the static link work, and both are easy to lose:
#
#   - libtool reads -static as "link the project's own .la libraries
#     statically" and leaves the rest dynamic. -all-static is the flag
#     that makes the whole binary static, and the binary that comes
#     out of -static alone still names /lib/ld-musl-x86_64.so.1 as its
#     interpreter, so `file` is the check.
#   - No distribution ships a static libdbus, so dbus is built here
#     first and BlueZ links against the libdbus-1.a that build
#     installs.

FROM alpine:3.22 AS daemons

ARG BLUEZ_VERSION=5.82
ARG BLUEZ_SHA256=0739fa608a837967ee6d5572b43fb89946a938d1c6c26127158aaefd743a790b
ARG DBUS_VERSION=1.16.2
ARG DBUS_SHA256=0ba2a1a4b16afe7bceb2c07e9ce99a8c2c3508e5dec290dbb643384bd6beb7e2

RUN apk add --no-cache \
        build-base pkgconf meson ninja wget tar xz linux-headers \
        expat-dev expat-static \
        glib-dev glib-static pcre2-dev pcre2-static libffi-dev zlib-static \
        eudev-dev json-c-dev readline-dev readline-static ncurses-static

# dbus comes first. BlueZ links against libdbus-1.a, and this install
# is where that archive, its headers, and its pkg-config file come
# from. dbus 1.16 builds with meson only, and prefer_static makes the
# link pick expat's archive over its shared object.
WORKDIR /build
RUN wget -q "https://dbus.freedesktop.org/releases/dbus/dbus-${DBUS_VERSION}.tar.xz" \
    && echo "${DBUS_SHA256}  dbus-${DBUS_VERSION}.tar.xz" | sha256sum -c - \
    && tar xf "dbus-${DBUS_VERSION}.tar.xz"
WORKDIR /build/dbus-${DBUS_VERSION}
RUN meson setup build \
        --prefix=/usr --sysconfdir=/etc --localstatedir=/var \
        --default-library=static -Dprefer_static=true \
        -Dmodular_tests=disabled -Dxml_docs=disabled -Ddoxygen_docs=disabled \
        -Dsystemd=disabled -Dselinux=disabled -Dapparmor=disabled -Dlibaudit=disabled \
        -Dc_link_args=-static \
    && meson compile -C build \
    && meson install -C build --destdir /staging \
    && meson install -C build

# BlueZ. The disabled parts are the ones no controller on this bus
# needs: the CUPS printer backend and the OBEX file-transfer daemon.
# bluetoothctl stays, because a kubectl exec session with it is how a
# person inspects the running daemon by hand. Pairing itself is the
# operator's job, over D-Bus, from a PairingRequest.
#
# The monitor and the tools build, and the install step below takes
# btmon out of monitor/ and btmgmt out of tools/, and leaves every
# other program the two directories produce. --enable-deprecated
# stays off, so the hciconfig-era tools never build: they drive the
# raw HCI socket while bluetoothd drives the management channel, so
# a command from one of them and the state the daemon holds
# disagree.
#
# LIBS includes -lncursesw because readline's pkg-config file names no
# terminal library, and a static bluetoothctl then has no tputs.
WORKDIR /build
RUN wget -q "https://mirrors.edge.kernel.org/pub/linux/bluetooth/bluez-${BLUEZ_VERSION}.tar.xz" \
    && echo "${BLUEZ_SHA256}  bluez-${BLUEZ_VERSION}.tar.xz" | sha256sum -c - \
    && tar xf "bluez-${BLUEZ_VERSION}.tar.xz"
WORKDIR /build/bluez-${BLUEZ_VERSION}
RUN ./configure \
        --prefix=/usr --sysconfdir=/etc --localstatedir=/var --libexecdir=/usr/libexec \
        --enable-client --enable-datafiles \
        --enable-monitor --enable-tools \
        --disable-manpages --disable-cups --disable-obex --disable-systemd \
    && make -j"$(nproc)" LDFLAGS="-all-static" LIBS="-lncursesw" \
    && make install DESTDIR=/staging

# Every file the image ships, named. install -D fails on a path that
# moved, so a BlueZ or dbus release that relocates one of these stops
# the build rather than shipping an image without it.
#
# The four person-run tools each answer a question nothing else in
# the pod can. btmgmt reads the kernel's management settings, and
# btmgmt info is the only reading of Connectable, Discoverable, and
# the rest this image can take. btmon traces the HCI link, the
# layer under D-Bus and under the daemon, which is the only reading
# of what the controller and the remote device exchanged; the bind
# it makes tests CAP_NET_RAW, which the pod's bluetoothd container
# adds for it. dbus-send calls any method on org.bluez, so a person
# starts one connection or reads one property with no shell.
#
# The three additions cost 3,042,456 bytes on a 7,851,292-byte
# image: btmgmt 500,008, btmon 2,114,552, and dbus-send 427,896,
# each after strip. btmon is the largest because its decoder tables
# name every HCI command, event, and profile. dbus-monitor stays
# out: it costs 415,608 bytes and watches the same events btmon
# already reports, one layer up.
#
# btmgmt is a noinst_PROGRAMS entry in Makefile.tools, so make
# install leaves it in the build tree, and the copy below takes it
# from there. A BlueZ release that moves it stops the build.
RUN set -eux; \
    install -D -m 0755 /staging/usr/libexec/bluetooth/bluetoothd /out/usr/libexec/bluetooth/bluetoothd; \
    install -D -m 0755 /staging/usr/bin/bluetoothctl /out/usr/bin/bluetoothctl; \
    install -D -m 0755 /staging/usr/bin/btmon /out/usr/bin/btmon; \
    install -D -m 0755 "/build/bluez-${BLUEZ_VERSION}/tools/btmgmt" /out/usr/bin/btmgmt; \
    install -D -m 0755 /staging/usr/bin/dbus-daemon /out/usr/bin/dbus-daemon; \
    install -D -m 0755 /staging/usr/bin/dbus-send /out/usr/bin/dbus-send; \
    strip /out/usr/libexec/bluetooth/bluetoothd /out/usr/bin/bluetoothctl \
        /out/usr/bin/btmon /out/usr/bin/btmgmt \
        /out/usr/bin/dbus-daemon /out/usr/bin/dbus-send; \
    for binary in /out/usr/libexec/bluetooth/bluetoothd /out/usr/bin/bluetoothctl \
        /out/usr/bin/btmon /out/usr/bin/btmgmt \
        /out/usr/bin/dbus-daemon /out/usr/bin/dbus-send; do \
        file "$binary" | grep -q "statically linked" || { file "$binary"; exit 1; }; \
    done

# dbus's own system.conf, unchanged, and the policy file BlueZ
# installs into it. system.conf denies every name and every method
# call, and bluetooth.conf is the hole that lets root own org.bluez.
# Writing a policy by hand here would mean re-deriving that pair.
RUN set -eux; \
    install -D -m 0644 /staging/usr/share/dbus-1/system.conf /out/usr/share/dbus-1/system.conf; \
    install -D -m 0644 /staging/usr/share/dbus-1/system.d/bluetooth.conf /out/usr/share/dbus-1/system.d/bluetooth.conf

# dbus-daemon drops to its messagebus user as soon as it has bound the
# socket, and it looks the name up in /etc/passwd. musl resolves users
# from those files directly, with no NSS modules to load, so these two
# files are the whole of what the lookup reads. The shells name a path
# that is not in the image, because nothing here ever starts one.
RUN set -eux; \
    mkdir -p /out/etc /out/etc/bluetooth /out/var/lib/bluetooth; \
    printf 'root:x:0:0:root:/root:/sbin/nologin\nmessagebus:x:100:100:D-Bus message bus:/:/sbin/nologin\n' > /out/etc/passwd; \
    printf 'root:x:0:\nmessagebus:x:100:\n' > /out/etc/group

# The entrypoint. It is built from this repository's own module, which
# is also where the operator comes from, so both images move together.
FROM golang:1.26.5-bookworm AS entrypoint
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY bluetoothd/*.go ./bluetoothd/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /start-bluetoothd ./bluetoothd

FROM scratch
COPY --from=daemons /out/ /
COPY --from=entrypoint /start-bluetoothd /usr/local/bin/start-bluetoothd

# AutoEnable is baked in, because an adapter that this operator holds
# must power itself on, and no deployment has a reason to say
# otherwise. input.conf is not here: the entrypoint writes it at start
# from BLUETOOTH_CLASSIC_BONDED_ONLY, because that setting is a
# security choice a person makes for one pairing session.
COPY bluetoothd/main.conf /etc/bluetooth/main.conf

# The image's own default for the bus address. The pod states the same
# address on both containers, next to the volume that holds the
# socket, and a bluetoothctl session started with kubectl exec reads
# whichever of the two applies.
ENV DBUS_SYSTEM_BUS_ADDRESS=unix:path=/var/run/bluetooth.liken.sh/dbus/system_bus_socket

ENTRYPOINT ["/usr/local/bin/start-bluetoothd"]
