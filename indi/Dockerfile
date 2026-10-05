# The INDI images: indi, with indiserver and every core driver, and one
# image on top of it for each vendor SDK family, the third-party drivers
# with no SDK, and the simulators. Each image is a library closure on
# scratch: the programs of its list in images/, every library the
# loader resolves for them, and the files they read by name. That is
# 71 MB for indi, where Ubuntu with the same packages is about 1 GB.
#
# Everything installs from dated snapshots of two archives: Ubuntu
# 26.04 from snapshot.ubuntu.com, and the INDI PPA from
# snapshot.ppa.launchpadcontent.net. The PPA itself keeps only its
# newest build of each package, so only a snapshot builds the same
# files again. VERSION, the date in package.toml, names both snapshots.

FROM ubuntu:26.04@sha256:f144425ff09be612d6d9ad965196e9cdc23dae1f42110a8a11a3e9a8198759f7 AS builder
ARG VERSION
# The snapshot services answer only over HTTPS, and the Ubuntu image
# has no CA bundle. Mozilla's bundle, as curl.se publishes it, comes in
# by its checksum, so the build fetches the same file every time. It
# stays in the builder; only indi-open ships it, for its weather drivers.
ADD --chmod=644 --checksum=sha256:a41b5d356aea97a529fe27e0f7316d2f9d946d75927476cf9cf1b90637d00505 \
    https://curl.se/ca/cacert-2026-09-25.pem /etc/apt/cacert.pem
COPY mutlaqja.asc /etc/apt/trusted.gpg.d/mutlaqja.asc
COPY snapshot.sh /snapshot.sh
RUN sh /snapshot.sh "$VERSION" \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        indi-bin \
        indi-3rdparty-drivers \
        indi-3rdparty-libs \
        gsc \
        gsc-data \
        socat \
        dash \
    && rm -rf /var/lib/apt/lists/*
COPY closure.sh indi-closure.sh family.sh check-lists.sh /
COPY images /images
RUN sh /check-lists.sh /images

# The shim that stands in for a driver in another pod. It is static, so
# it needs nothing from the closure.
FROM golang:1.27.1-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS shim
WORKDIR /src
COPY shim/go.mod shim/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags=-s -o /indi-shim .

FROM builder AS indi-tree
# /etc/indi-versions records the packages the closure came from, and
# the smoke check compares it with [package.upstream] in package.toml.
# A driver writes its configuration under $HOME/.indi, and the image
# has no home directory, so HOME is /tmp.
RUN sh /indi-closure.sh /out /images/indi \
    && dpkg-query -W indi-bin indi-3rdparty-drivers gsc > /out/etc/indi-versions \
    && mkdir -p -m 1777 /out/tmp
COPY --from=shim /indi-shim /out/usr/bin/indi-shim

FROM scratch AS indi
COPY --from=indi-tree /out /
# socat starts a driver by name, and the CCD simulator runs gsc by
# name, so the image states the one directory that holds programs.
ENV PATH=/usr/bin HOME=/tmp
EXPOSE 7624
ENTRYPOINT ["/usr/bin/indiserver"]

FROM builder AS open-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh open
FROM indi AS indi-open
COPY --from=open-tree /out /

FROM builder AS zwo-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh zwo
FROM indi AS indi-zwo
COPY --from=zwo-tree /out /

FROM builder AS qhy-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh qhy
FROM indi AS indi-qhy
COPY --from=qhy-tree /out /

FROM builder AS playerone-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh playerone
FROM indi AS indi-playerone
COPY --from=playerone-tree /out /

FROM builder AS svbony-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh svbony
FROM indi AS indi-svbony
COPY --from=svbony-tree /out /

FROM builder AS atik-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh atik
FROM indi AS indi-atik
COPY --from=atik-tree /out /

FROM builder AS fli-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh fli
FROM indi AS indi-fli
COPY --from=fli-tree /out /

FROM builder AS sbig-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh sbig
FROM indi AS indi-sbig
COPY --from=sbig-tree /out /

FROM builder AS mi-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh mi
FROM indi AS indi-mi
COPY --from=mi-tree /out /

FROM builder AS qsi-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh qsi
FROM indi AS indi-qsi
COPY --from=qsi-tree /out /

FROM builder AS apogee-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh apogee
FROM indi AS indi-apogee
COPY --from=apogee-tree /out /

FROM builder AS astroasis-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh astroasis
FROM indi AS indi-astroasis
COPY --from=astroasis-tree /out /

FROM builder AS gphoto-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh gphoto
FROM indi AS indi-gphoto
COPY --from=gphoto-tree /out /

FROM builder AS touptek-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh touptek
FROM indi AS indi-touptek
COPY --from=touptek-tree /out /

FROM builder AS simulators-tree
COPY --from=indi-tree /out /base
RUN sh /family.sh simulators
FROM indi AS indi-simulators
COPY --from=simulators-tree /out /
