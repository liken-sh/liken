# The bluetoothd image: the static programs the pod runs (bluetoothd,
# dbus-daemon, btmon, and the three Go entrypoints, start-bluetoothd,
# start-btmon, and start-rescan), three more that a person runs with
# kubectl exec (bluetoothctl, btmgmt, and dbus-send), their
# configuration, and nothing else.
#
# bluetoothd ships here, in a workload's image, and not in the
# read-only root that every liken machine boots. That is why the
# device operator pattern puts the daemon in a separate repository, and
# it is why this image builds BlueZ rather than installing a
# distribution's package: a package brings a package manager, a shell,
# and a base filesystem, and this container runs three programs.
#
# buildroot builds BlueZ, dbus, and every library they link, from the
# BR2_EXTERNAL tree in liken/userland, the same tree and the same
# buildroot.sh that build the static programs of the OS.
# bluetoothd.config in that tree selects the programs, and
# bluetooth/bluez-static in it holds the BlueZ recipe and the patches
# that the build applies to BlueZ. The README.md beside the patches
# lists each one and the bug it fixes. buildroot checks every source
# tarball against a .hash file, so this build reads no byte that a
# commit did not name.
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
# binaries here open none, so no version can disagree. The build checks
# each binary with file(1) and accepts "statically linked" or
# "static-pie linked".

FROM buildroot AS daemons

# The buildroot commit and the digest of its tarball are the pin in
# liken/userland/VERSION and liken/userland/fetch.sh, and
# liken/userland/latest.sh --bump writes the two lines below with
# them. `cd ci && go run . pins -root ..` fails when they disagree.
ARG BUILDROOT_COMMIT=c1490f289c129c12de1249074eac765dee1a5d9d
ARG BUILDROOT_SHA256=34923d316a19c4acbffc8fffa2281a99e9f6666c43b3bfe1c06c2c491cdff262

# The buildroot image runs as br-user, and buildroot writes its output
# and its caches as that user. The download directory and the compiler
# cache are cache mounts, so a rebuild on the same machine downloads
# and compiles only what changed.
WORKDIR /home/br-user
ADD --chown=br-user:br-user --checksum=sha256:${BUILDROOT_SHA256} \
    https://github.com/buildroot/buildroot/archive/${BUILDROOT_COMMIT}.tar.gz buildroot.tar.gz
RUN tar -xzf buildroot.tar.gz && mv "buildroot-${BUILDROOT_COMMIT}" buildroot && rm buildroot.tar.gz
COPY --from=userland --chown=br-user:br-user . external/
RUN --mount=type=cache,target=/home/br-user/dl,uid=1000,gid=1000 \
    --mount=type=cache,target=/home/br-user/ccache,uid=1000,gid=1000 \
    BR2_DL_DIR=/home/br-user/dl BR2_CCACHE_DIR=/home/br-user/ccache \
    bash external/buildroot.sh /home/br-user/buildroot /home/br-user/output bluetoothd.config

# Every file the image ships, named. install -D fails on a path that
# moved, so a BlueZ or dbus release that relocates one of these stops
# the build rather than shipping an image without it. buildroot strips
# each binary in its target tree.
#
# The four tools each answer a question nothing else in the pod can.
# btmgmt reads the kernel's management settings, and btmgmt info is
# the only reading of Connectable, Discoverable, and the rest this
# image can take. btmon traces the HCI link, the layer under D-Bus
# and under the daemon, which is the only reading of what the
# controller and the remote device exchanged. The pod's btmon
# container runs it while a person has the trace turned on, and that
# container holds the CAP_NET_RAW that btmon's bind tests. dbus-send
# calls any method on org.bluez, so a person starts one connection or
# reads one property with no shell.
#
# Each tool adds its whole static binary to every node that runs the
# pod, so a tool goes in only when it answers one of those questions.
# btmon is the largest of the three because its decoder tables name
# every HCI command, event, and profile. dbus-monitor stays out
# because it watches the same events btmon already reports, one layer
# up. To see what each tool costs, build the image with
# `docker buildx bake bluetoothd --load` at the top of the repository.
# The image has no shell, so list its files from outside: `docker
# create` a container of it, and `docker export` that container into
# `tar -tv`, which prints each binary with its size. `docker image
# inspect --format '{{.Size}}'` gives the size of the whole image.
RUN set -eu; \
    target=/home/br-user/output/target; \
    for binary in usr/libexec/bluetooth/bluetoothd usr/bin/bluetoothctl \
        usr/bin/btmon usr/bin/btmgmt usr/bin/dbus-daemon usr/bin/dbus-send; do \
        file "$target/$binary" | grep -qE "statically linked|static-pie linked" || { file "$target/$binary"; exit 1; }; \
        install -D -m 0755 "$target/$binary" "/home/br-user/out/$binary"; \
    done

# dbus's own system.conf, unchanged, and the policy file BlueZ
# installs into it. system.conf denies every name and every method
# call, and bluetooth.conf is the hole that lets root own org.bluez.
# Writing a policy by hand here would mean re-deriving that pair.
RUN set -eu; \
    target=/home/br-user/output/target; \
    install -D -m 0644 "$target/usr/share/dbus-1/system.conf" /home/br-user/out/usr/share/dbus-1/system.conf; \
    install -D -m 0644 "$(find "$target" -path '*/dbus-1/system.d/bluetooth.conf' | head -1)" \
        /home/br-user/out/usr/share/dbus-1/system.d/bluetooth.conf

# The licenses of BlueZ, dbus, and the libraries they link ask that
# their notices travel with the programs, and the GPL that BlueZ uses
# also requires that the source of a static program be available, the
# libraries it links included, and the changes made to it. So the
# image carries buildroot's legal-info manifest, which names each
# package, its version, its license, and where its source comes from,
# the license files of every package, and the patches applied to
# BlueZ.
RUN set -eu; \
    doc=/home/br-user/out/usr/share/doc/liken; \
    mkdir -p "$doc/bluez-patches"; \
    cp /home/br-user/output/legal-info/manifest.csv "$doc/manifest.csv"; \
    cp -r /home/br-user/output/legal-info/licenses "$doc/licenses"; \
    cp external/bluetooth/bluez-static/*.patch "$doc/bluez-patches/"

# dbus-daemon drops to its user, dbus, as soon as it has bound the
# socket, and it looks the name up in /etc/passwd. buildroot's dbus
# recipe names that user in system.conf. musl resolves users from
# those files directly, with no NSS modules to load, so these two
# files are the whole of what the lookup reads. The shells name a path
# that is not in the image, because nothing here ever starts one.
RUN set -eu; \
    out=/home/br-user/out; \
    mkdir -p "$out/etc/bluetooth" "$out/var/lib/bluetooth"; \
    printf 'root:x:0:0:root:/root:/sbin/nologin\ndbus:x:100:100:D-Bus message bus:/:/sbin/nologin\n' > "$out/etc/passwd"; \
    printf 'root:x:0:\ndbus:x:100:\n' > "$out/etc/group"

# The three entrypoints. They are built from this repository's own
# module, which is also where the operator comes from, so the images
# move together. start-btmon is the command of the pod's btmon
# container, and it runs btmon while the Adapter's spec.btmon is true.
# start-rescan is the command of the pod's rescan container, and it
# works around a kernel bug that leaves the passive scan off after a
# failed connection (bluetoothd/rescan/main.go).
FROM golang AS entrypoint
# proxy.golang.org sometimes drops a module download partway through
# the stream. With the default comma, `go` tries the origin only when
# the proxy answers 404 or 410, so a dropped stream fails the build.
# With a pipe, `go` fetches the module from its origin after any proxy
# error, with the git that this image includes. `GOSUMDB` keeps its
# default, so the checksum database still verifies a module that `go`
# fetched from its origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
COPY go.mod go.sum ./
# go.mod replaces the shared kubernetes module with ../kubernetes, so
# the module graph reads its go.mod from the second build context named
# kubernetes. The entrypoints import none of its packages, so the
# module file is all they need.
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
# the binaries link. notices/go.sh says why the image needs them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices ./bluetoothd ./bluetoothd/btmon ./bluetoothd/rescan

FROM scratch
COPY --from=daemons /home/br-user/out/ /
COPY --from=entrypoint /start-bluetoothd /start-bluetoothd
COPY --from=entrypoint /start-btmon /start-btmon
COPY --from=entrypoint /start-rescan /start-rescan
COPY --from=entrypoint /notices /

# /etc/bluetooth holds no file in the image. The entrypoint writes
# main.conf and input.conf at start: main.conf takes its Privacy key
# from the Adapter, and input.conf takes ClassicBondedOnly from
# BLUETOOTH_CLASSIC_BONDED_ONLY. Each one is a choice a person makes
# at deploy time.

# The image's own default for the bus address. The pod states the same
# address on both containers, next to the volume that holds the
# socket, and a bluetoothctl session started with kubectl exec reads
# whichever of the two applies.
ENV DBUS_SYSTEM_BUS_ADDRESS=unix:path=/var/run/bluetooth.liken.sh/dbus/system_bus_socket

ENTRYPOINT ["/start-bluetoothd"]
