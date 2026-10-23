# The scan image: fwupd and the one small program, `scan`, that firmware-operator
# runs in a privileged pod on a machine. The program downloads one
# catalog, hands it to fwupdtool, and prints which firmware devices the
# machine has and which releases the catalog offers for them. As
# `scan rehearse`, in a pod that is not privileged, it runs an approved
# install against a copy of the machine's firmware variables. As
# `scan apply`, in a pod that is not privileged either, it rehearses
# again, waits for the operator's signal, and writes the rehearsed append
# to the machine's variable, and as `scan prepared` it is that pod's
# readiness probe. The image
# is Debian, not scratch, because fwupd links dozens of libraries and
# reads plugin and quirk data from the files of its package.

FROM golang:1.27.3-bookworm AS build
# proxy.golang.org sometimes drops a module download partway through
# the stream. With the default comma, `go` tries the origin only when
# the proxy answers 404 or 410, so a dropped stream fails the build.
# With a pipe, `go` fetches the module from its origin after any proxy
# error, with the git that this image includes. `GOSUMDB` keeps its
# default, so the checksum database still verifies a module that `go`
# fetched from its origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
# go.mod replaces the shared kubernetes module with ../kubernetes, and
# the build takes that directory as a second build context named
# kubernetes. It lands at /kubernetes, the same place relative to /src
# that kubernetes/ has relative to this component. The scan imports
# nothing from either module, so the build needs only their module
# files to resolve the graph, and no download.
COPY go.mod go.sum ./
COPY --from=kubernetes go.mod go.sum /kubernetes/
COPY scan/ ./scan/
# CGO_ENABLED=0 with -trimpath is liken's own build discipline: a
# static binary with no paths from the build machine in it.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /scan ./scan
# The license files of the Go standard library. notices/go.sh says why
# the image needs them.
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices ./scan

# fwupd comes from Debian 13's backports, which carry new upstream
# releases of fwupd onto the stable release. fwupd publishes only its
# source for Linux, so a distribution's package is the nearest one to
# upstream. The scan needs fwupd 2.1.6 or later: a KEK release on the
# LVFS names the machine's platform key as another device that must be
# present, and fwupd 2.1.6 checks each release's requirements again
# after it has added every device. An older fwupd checks them once,
# when it adds the KEK device, before the platform key's device exists,
# so it never offers the KEK release. The image is the Debian image
# that the base images of liken pin.
FROM debian:trixie-slim@sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f
# fwupd-amd64-signed holds fwupd-efi's updater, fwupdx64.efi, which a
# capsule's install in NVRAM mode copies to the ESP: the firmware boots
# it once, and it hands the capsule to the firmware.
#
# The date of the snapshot.debian.org archive that fwupd installs from.
# A snapshot gives the same fwupd to every build, so a change in what a
# scan finds comes from the machine or the catalog and never from a new
# package. A new fwupd is a new date here.
ENV SNAPSHOT=20261019
# The sources name https, with the CA bundle that the trust component
# pins for every image of liken, so a host on the path cannot serve an
# older InRelease file that Debian also signed. The Release files of an
# old snapshot are past their Valid-Until date, so apt must not check
# it. apt still checks every Release file against the Debian archive
# keys. The scan program also reads the bundle, to download the
# catalog.
COPY --from=trust / /
RUN echo 'Acquire::https::CAInfo "/etc/ssl/certs/ca-certificates.crt";' >/etc/apt/apt.conf.d/99snapshot-ca \
    && rm -f /etc/apt/sources.list /etc/apt/sources.list.d/* \
    && printf '%s\n' \
        'Types: deb' \
        "URIs: https://snapshot.debian.org/archive/debian/${SNAPSHOT}T000000Z/" \
        'Suites: trixie trixie-updates trixie-backports' \
        'Components: main' \
        'Signed-By: /usr/share/keyrings/debian-archive-keyring.pgp' \
        'Check-Valid-Until: no' \
        '' \
        'Types: deb' \
        "URIs: https://snapshot.debian.org/archive/debian-security/${SNAPSHOT}T000000Z/" \
        'Suites: trixie-security' \
        'Components: main' \
        'Signed-By: /usr/share/keyrings/debian-archive-keyring.pgp' \
        'Check-Valid-Until: no' \
        >/etc/apt/sources.list.d/snapshot.sources \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        -t trixie-backports fwupd fwupd-amd64-signed \
    && rm -rf /var/lib/apt/lists/*
# The image holds the files of Debian packages, unmodified, and their
# licenses require that each package's copyright file travel with it.
# The Debian image keeps those files under /usr/share/doc. This list
# names each package so that notices/check.sh can prove it, and points
# a reader to the source. A few packages, such as libgcc-s1, make their
# /usr/share/doc directory a link to the directory of a package of the
# same source. That package is on the list and holds the copyright file,
# so the list skips the linking ones.
RUN mkdir -p /usr/share/doc/liken \
    && { \
        echo 'ghcr.io/liken-sh/firmware-operator-scan holds files of the Debian packages below,'; \
        echo 'unmodified. /usr/share/doc/<package>/copyright states the copyright'; \
        echo 'and the license of each package. snapshot.debian.org serves the'; \
        echo 'source of each package at the date of the snapshot.'; \
        echo; \
        echo '<package> <version> <source> <source version>'; \
        dpkg-query -W -f '${Package} ${Version} ${source:Package} ${source:Version}\n' \
            | while read -r package rest; do \
                if [ ! -L "/usr/share/doc/$package" ]; then echo "$package $rest"; fi; \
            done; \
    } >/usr/share/doc/liken/firmware-operator-scan.packages
COPY --from=build /scan /usr/local/bin/scan
COPY --from=build /notices /
# The scan pod is privileged and runs as root, because fwupd reads
# SMBIOS tables, efivarfs, sysfs, and device nodes. The rehearsal pod
# runs as root with only DAC_READ_SEARCH, and reads a read-only mount.
ENTRYPOINT ["/usr/local/bin/scan"]
