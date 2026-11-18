# The image of the pod's Bluetooth stack: bluetoothd, dbus-daemon,
# btmon, the Go entrypoints, and the tools a person runs with
# kubectl exec. bluetoothd runs here and not in the read-only root of
# the liken OS, so the OS image needs no Bluetooth stack.
#
# buildroot builds BlueZ and dbus as static binaries, from the
# BR2_EXTERNAL tree in liken/userland and with the same buildroot.sh
# that builds the OS's programs. A distribution's package would bring a
# package manager, a shell, and a base filesystem that the container
# does not use. bluetoothd.config in that tree selects the programs,
# and bluetooth/bluez-static holds the BlueZ recipe and its patches.
# The README.md beside the patches lists each patch and the bug it
# fixes.
#
# bluetoothctl and btmgmt cannot run a command that takes an argument
# in this image. BlueZ parses the argument specification of each
# command with wordexp(3), and musl implements wordexp by running
# /bin/sh. This image has no /bin/sh, so wordexp returns WRDE_SYNTAX
# and both programs fail with "Unable to parse mandatory command
# arguments". A command with an empty specification skips the call and
# works: bluetoothctl list, and btmgmt info, extinfo, con, keys, and
# ltks. dbus-send parses its arguments with getopt and starts no
# shell, so it can call every org.bluez method and read every property
# that bluetoothctl and btmgmt cannot.

# BuildKit keeps a cache mount on one builder and never writes it into
# the registry's layer cache. A CI runner starts with an empty builder,
# so a build that misses the layer cache would compile the
# cross-compiler and every package again. CI runs cache-seed before the
# image's build, to copy the compiler cache that CI restored (the build
# context `cache`) into the mount, and cache-export after it, to write
# the mount's files out for CI to save. The image does not use either
# stage. Both run as uid 1000, br-user in the buildroot image, which
# owns the cache mount.
FROM debian AS cache-seed
USER 1000:1000
RUN --mount=type=cache,target=/home/br-user/ccache,uid=1000,gid=1000 \
    --mount=type=bind,from=cache,target=/seed \
    cp -r /seed/. /home/br-user/ccache/

FROM debian AS cache-files
USER 1000:1000
RUN --mount=type=cache,target=/home/br-user/ccache,uid=1000,gid=1000 \
    mkdir /tmp/ccache && cp -r /home/br-user/ccache/. /tmp/ccache/

FROM scratch AS cache-export
COPY --from=cache-files /tmp/ccache/ /

FROM buildroot AS daemons

# The buildroot commit and the digest of its tarball are the pin in
# liken/userland/VERSION and liken/userland/fetch.sh.
# liken/userland/latest.sh --bump writes the two lines below with them,
# and `cd ci && go run . pins -root ..` fails when they differ.
ARG BUILDROOT_COMMIT=c1490f289c129c12de1249074eac765dee1a5d9d
ARG BUILDROOT_SHA256=34923d316a19c4acbffc8fffa2281a99e9f6666c43b3bfe1c06c2c491cdff262

WORKDIR /home/br-user
ADD --chown=br-user:br-user --checksum=sha256:${BUILDROOT_SHA256} \
    https://github.com/buildroot/buildroot/archive/${BUILDROOT_COMMIT}.tar.gz buildroot.tar.gz
RUN tar -xzf buildroot.tar.gz && mv "buildroot-${BUILDROOT_COMMIT}" buildroot && rm buildroot.tar.gz

# Copy only the files of the userland tree that this build reads, so a
# change to a file that only the OS build reads, such as os.config,
# build.sh, or artifact.sh, does not invalidate these layers and
# rebuild the cross-compiler. iscsi/ is copied because Config.in and
# external.mk include its recipes, and patches/ because common.config
# sets BR2_GLOBAL_PATCH_DIR to it.
COPY --from=userland --chown=br-user:br-user \
    buildroot.sh common.config bluetoothd.config \
    Config.in external.desc external.mk versions.mk \
    external/
COPY --from=userland --chown=br-user:br-user bluetooth/ external/bluetooth/
COPY --from=userland --chown=br-user:br-user iscsi/ external/iscsi/
COPY --from=userland --chown=br-user:br-user patches/ external/patches/

# show-info writes the package list that the SBOM step below reads.
# The step then deletes the cross-compiler, the build directories, and
# the source tarballs that legal-info copied. Later steps read only the
# target tree and legal-info's manifest and license files, and the
# deleted directories take several gigabytes that CI would otherwise
# push into the registry's layer cache. CCACHE_MAXSIZE keeps the
# compiler cache that CI saves from growing with each build.
RUN --mount=type=cache,target=/home/br-user/dl,uid=1000,gid=1000 \
    --mount=type=cache,target=/home/br-user/ccache,uid=1000,gid=1000 \
    set -eu; \
    BR2_DL_DIR=/home/br-user/dl BR2_CCACHE_DIR=/home/br-user/ccache CCACHE_MAXSIZE=500M \
        bash external/buildroot.sh /home/br-user/buildroot /home/br-user/output bluetoothd.config; \
    make --no-print-directory -s -C buildroot O=/home/br-user/output show-info >/home/br-user/show-info.json; \
    cd /home/br-user/output; \
    rm -rf build host staging per-package images legal-info/sources legal-info/host-sources

# generate-cyclonedx writes buildroot's SBOM of this build from the
# package list, as liken/userland/build.sh does for the OS. It reads
# the SPDX license list of its CycloneDX version, and downloads the
# list when its download directory has none. So this step adds the list
# by the version and digest in the spdx_schema_sha256 table of
# liken/userland/fetch.sh. liken/userland/latest.sh --bump writes the
# two ARG lines below, and `cd ci && go run . pins -root ..` fails when
# they differ from the table. They come after the package build so that
# a new list does not rebuild the packages. The `test` fails the build
# when the pinned buildroot targets another CycloneDX version, because
# generate-cyclonedx would then download that version's list unpinned.
ARG SPDX_SCHEMA_VERSION=1.6.2
ARG SPDX_SCHEMA_SHA256=c41917196639055e9f9670811bac23ef777732144f3ff5a2f39686f61580dbe6
ADD --chown=br-user:br-user --checksum=sha256:${SPDX_SCHEMA_SHA256} \
    https://raw.githubusercontent.com/CycloneDX/specification/${SPDX_SCHEMA_VERSION}/schema/spdx.schema.json \
    sbom-dl/cyclonedx/spdx-${SPDX_SCHEMA_VERSION}.schema.json
RUN set -eu; \
    cd buildroot; \
    test "$(sed -n 's/^DEFAULT_CYCLONEDX_VERSION = "\(.*\)"$/\1/p' utils/generate-cyclonedx)" = "$SPDX_SCHEMA_VERSION"; \
    BR2_DL_DIR=/home/br-user/sbom-dl utils/generate-cyclonedx \
        </home/br-user/show-info.json >/home/br-user/output/sbom.cdx.json

# Copy each file the image ships by name. `install -D` fails when a
# path does not exist, so a BlueZ or dbus release that moves one of
# these files fails the build. buildroot already strips the binaries in
# its target tree.
#
# Each tool adds its whole static binary to every node that runs the
# pod, so the image ships a tool only when it shows something that
# nothing else in the pod can. `btmgmt info` is the only way to read
# the kernel's controller settings, such as Connectable and
# Discoverable. btmon traces the HCI traffic between the controller and
# the remote device, below D-Bus and bluetoothd. The pod's btmon
# container runs it while a person has the trace turned on. dbus-send
# calls any method on org.bluez. dbus-monitor is left out because btmon
# already shows the same exchanges one layer lower.
#
# The image has no shell, so to see each file's size, `docker create`
# a container from it and pipe `docker export` into `tar -tv`.
RUN set -eu; \
    target=/home/br-user/output/target; \
    for binary in usr/libexec/bluetooth/bluetoothd usr/bin/bluetoothctl \
        usr/bin/btmon usr/bin/btmgmt usr/bin/dbus-daemon usr/bin/dbus-send; do \
        file "$target/$binary" | grep -qE "statically linked|static-pie linked" || { file "$target/$binary"; exit 1; }; \
        install -D -m 0755 "$target/$binary" "/home/br-user/out/$binary"; \
    done

# Ship dbus's own system.conf and BlueZ's bluetooth.conf unchanged.
# system.conf denies every name and every method call by default, and
# bluetooth.conf allows root to own org.bluez.
RUN set -eu; \
    target=/home/br-user/output/target; \
    install -D -m 0644 "$target/usr/share/dbus-1/system.conf" /home/br-user/out/usr/share/dbus-1/system.conf; \
    install -D -m 0644 "$(find "$target" -path '*/dbus-1/system.d/bluetooth.conf' | head -1)" \
        /home/br-user/out/usr/share/dbus-1/system.d/bluetooth.conf

# The licenses of BlueZ, dbus, and the libraries they link require that
# the notices ship with the programs. The GPL that BlueZ uses also
# requires that the source of a static program, with the libraries it
# links and the changes made to it, be available. So the image ships
# buildroot's legal-info manifest, which lists each package with its
# version, license, and source location, the license files of every
# package, and the patches applied to BlueZ.
RUN set -eu; \
    doc=/home/br-user/out/usr/share/doc/liken; \
    mkdir -p "$doc/bluez-patches"; \
    cp /home/br-user/output/legal-info/manifest.csv "$doc/manifest.csv"; \
    cp -r /home/br-user/output/legal-info/licenses "$doc/licenses"; \
    cp external/bluetooth/bluez-static/*.patch "$doc/bluez-patches/"

# dbus-daemon switches to the user dbus, which buildroot's recipe sets
# in system.conf, as soon as it binds the socket. musl looks the user
# up in /etc/passwd and /etc/group with no NSS modules, so these two
# files are enough. /sbin/nologin is not in the image, and nothing here
# starts a login shell.
RUN set -eu; \
    out=/home/br-user/out; \
    mkdir -p "$out/etc/bluetooth" "$out/var/lib/bluetooth"; \
    printf 'root:x:0:0:root:/root:/sbin/nologin\ndbus:x:100:100:D-Bus message bus:/:/sbin/nologin\n' > "$out/etc/passwd"; \
    printf 'root:x:0:\ndbus:x:100:\n' > "$out/etc/group"

# The entrypoints are built from the operator's own module, so the
# operator image and this image come from the same commit. start-btmon
# runs btmon while the Adapter's spec.btmon is true. start-rescan works
# around kernel bugs that leave the passive scan off after a failed
# connection (bluetoothd/rescan/main.go).
FROM golang AS entrypoint
# proxy.golang.org sometimes drops a module download partway through.
# With the default comma, `go` falls back to the origin only when the
# proxy responds 404 or 410, so a dropped download fails the build.
# With a pipe, `go` falls back after any proxy error. The checksum
# database still verifies a module fetched from its origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
COPY go.mod go.sum ./
# go.mod replaces the kubernetes module with ../kubernetes, so
# `go mod download` needs that module's go.mod, from the build context
# kubernetes. The entrypoints import none of its packages.
COPY --from=kubernetes go.mod /kubernetes/
RUN go mod download
COPY bluetoothd/*.go ./bluetoothd/
COPY bluetoothd/btmon/*.go ./bluetoothd/btmon/
COPY bluetoothd/mgmt/*.go ./bluetoothd/mgmt/
COPY bluetoothd/rescan/*.go ./bluetoothd/rescan/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /start-bluetoothd ./bluetoothd
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /start-btmon ./bluetoothd/btmon
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /start-rescan ./bluetoothd/rescan
# The license files of the Go standard library and of each module that
# the binaries link. See notices/go.sh for why the image needs them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices ./bluetoothd ./bluetoothd/btmon ./bluetoothd/rescan

# syft finds no package in a static C program, so the image ships a
# CycloneDX record of the packages that buildroot built, and syft
# copies its components into the image's SBOM. image-sbom, from the
# build context manifest (toolchains/manifest), keeps the target
# packages of buildroot's SBOM and leaves out the host tools.
FROM golang AS record
WORKDIR /src
COPY --from=manifest . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /image-sbom ./image-sbom
COPY --from=daemons /home/br-user/output/sbom.cdx.json /buildroot.cdx.json
RUN /image-sbom -out /bluetoothd.cdx.json -buildroot /buildroot.cdx.json

FROM scratch
# The daemon's container runs as root without CAP_DAC_OVERRIDE, so it
# cannot write /etc/bluetooth/input.conf into a directory that br-user,
# buildroot's build user, owns. The image's files belong to root.
COPY --from=daemons --chown=0:0 /home/br-user/out/ /
COPY --from=entrypoint /start-bluetoothd /start-bluetoothd
COPY --from=entrypoint /start-btmon /start-btmon
COPY --from=entrypoint /start-rescan /start-rescan
COPY --from=entrypoint /notices /
COPY --from=record /bluetoothd.cdx.json /usr/share/doc/liken/bluetoothd.cdx.json

# /etc/bluetooth is empty in the image. start-bluetoothd writes
# main.conf and input.conf at start: Privacy from the Adapter's
# spec.privacy, and ClassicBondedOnly from
# BLUETOOTH_CLASSIC_BONDED_ONLY.

# bluetoothctl and dbus-send read the system bus address from this
# variable. The pod sets the same value in its containers.
ENV DBUS_SYSTEM_BUS_ADDRESS=unix:path=/var/run/bluetooth.liken.sh/dbus/system_bus_socket

ENTRYPOINT ["/start-bluetoothd"]
