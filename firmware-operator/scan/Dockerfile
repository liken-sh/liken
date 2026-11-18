# The image of the pods that firmware-operator runs on machines, with
# fwupd and the `scan` program. In a privileged pod, `scan` downloads a
# catalog, gives it to fwupdtool, and prints the machine's firmware
# devices and the releases that the catalog offers for them. In
# unprivileged pods, `scan rehearse` runs an approved install against a
# copy of the machine's firmware variables, and `scan apply` rehearses
# again, waits for the operator's signal, and then installs the update.
# `scan prepared` is the readiness probe of the apply pod. The image is
# Debian instead of scratch because fwupd links many shared libraries
# and reads plugin and quirk data from the files of its package.

FROM golang AS build
# proxy.golang.org sometimes drops a module download partway through.
# With the default comma, `go` falls back to the origin only when the
# proxy returns 404 or 410, so a dropped download fails the build. With
# a pipe, `go` falls back to the origin after any proxy error, using
# the git in this image. `GOSUMDB` keeps its default, so the checksum
# database still verifies a module that `go` fetched from its origin.
ENV GOPROXY="https://proxy.golang.org|direct"
WORKDIR /src
# go.mod replaces the shared kubernetes module with ../kubernetes, so
# its module files are copied to /kubernetes, the same path relative to
# /src. The scan program imports only the standard library, so the
# build needs no other file of either module.
COPY go.mod go.sum ./
COPY --from=kubernetes go.mod go.sum /kubernetes/
COPY scan/ ./scan/
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /scan ./scan
COPY --from=notices go.sh /
RUN CGO_ENABLED=0 sh /go.sh /notices ./scan

# fwupd comes from Debian sid, at the same snapshot date as the liken
# base images. Upstream fwupd publishes only source for Linux, so a
# distribution package is the closest to upstream.
#
# The scan needs fwupd 2.1.6 or later. A KEK release on the LVFS lists
# the machine's platform key as another device that must be present.
# fwupd 2.1.6 checks each release's requirements again after it adds
# every device. An older fwupd checks them once, when it adds the KEK
# device and before the platform key's device exists, so it never
# offers the KEK release.
#
# Every build from one snapshot installs the same fwupd, so a change in
# what a scan finds comes from the machine or the catalog, and a newer
# fwupd needs a newer snapshot date. The build context `builder` is the
# vulkan directory, for snapshot.sh, and SNAPSHOT is vulkan's version.
FROM debian
ARG SNAPSHOT
COPY --from=builder snapshot.sh snapshot.sha256 /
# The scan program verifies the catalog's HTTPS server with the CA
# bundle that the trust component pins for every liken image.
COPY --from=trust / /
# fwupd-amd64-signed contains fwupd-efi's updater, fwupdx64.efi. To
# install a capsule in NVRAM mode, fwupd copies this updater to the
# ESP. The firmware boots it once, and it passes the capsule to the
# firmware.
RUN sh /snapshot.sh "$SNAPSHOT" \
    && DEBIAN_FRONTEND=noninteractive apt-get upgrade -y \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        fwupd fwupd-amd64-signed \
    && rm -rf /var/lib/apt/lists/* /snapshot.sh /snapshot.sha256
# The licenses of the Debian packages require each package's copyright
# file, under /usr/share/doc, to ship with it. This list names each
# package so that notices/check.sh can verify the copyright files, and
# tells a reader where to find the source. It skips a package whose
# /usr/share/doc directory is a link, such as libgcc-s1, because the
# package that it links to is on the list and has the copyright file.
RUN mkdir -p /usr/share/doc/liken \
    && { \
        echo 'ghcr.io/liken-sh/firmware-operator-scan contains files of the Debian packages below,'; \
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
COPY --from=build /scan /scan
COPY --from=build /notices /
# The image sets no USER, because every pod that runs it runs as root.
# The scan pod is privileged, because fwupd reads SMBIOS tables,
# efivarfs, sysfs, and device nodes. The rehearsal and apply pods drop
# every capability except the ones that they add.
ENTRYPOINT ["/scan"]
