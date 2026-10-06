# The INDI images: indi, with indiserver and every core driver, and one
# image on top of it for each vendor SDK family, the third-party drivers
# with no SDK, and the simulators, and one image for PHD2, the guider,
# which builds on indi as a client of its server. Each image is a library closure on
# scratch: the programs of its list in images/, every library the
# loader resolves for them, and the files they read by name. That is
# 71 MB for indi, where Ubuntu with the same packages is about 1 GB.
#
# Everything installs from dated snapshots of three archives: Ubuntu
# 26.04 from snapshot.ubuntu.com, and the INDI PPA and the PHD2 PPA
# from snapshot.ppa.launchpadcontent.net. A PPA itself keeps only its
# newest build of each package, so only a snapshot builds the same
# files again. VERSION, the date in package.toml, names every snapshot.

FROM ubuntu:26.04@sha256:f144425ff09be612d6d9ad965196e9cdc23dae1f42110a8a11a3e9a8198759f7 AS builder
ARG VERSION
# The snapshot services answer only over HTTPS, and the Ubuntu image
# has no CA bundle. The bundle is the one that the trust component pins
# for every image of liken, with its notice. It stays in the builder;
# only indi-open ships it, for its weather drivers.
COPY --from=trust / /
COPY mutlaqja.asc pch.asc /etc/apt/trusted.gpg.d/
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
COPY sdk-licenses /sdk-licenses
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
# The shim links the Go standard library and no other module, and the
# Go license asks that its notice travel with the binary.
COPY --from=shim /usr/local/go/LICENSE /out/usr/share/doc/go/LICENSE

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

# PHD2 links GTK, wxWidgets, and OpenCV, about 300 MB of libraries that
# no driver needs. So only this stage installs it, and the builder that
# every other image starts from stays without it.
FROM builder AS phd2-tree
COPY --from=indi-tree /out /base
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends phd2 \
    && rm -rf /var/lib/apt/lists/*
RUN sh /family.sh phd2 \
    && { cat /base/etc/indi-versions; dpkg-query -W phd2; } >/out/etc/indi-versions \
    && mkdir -p /out/home/phd2 /out/etc/phd2 \
    && chown 1000:1000 /out/home/phd2
COPY phd2-start.sh /out/usr/bin/phd2-start
FROM indi AS indi-phd2
COPY --from=phd2-tree /out /
# PHD2 writes its config, its logs, and its instance lock under $HOME,
# and the pod mounts an emptyDir there. It reaches the compositor
# through the Wayland socket in $XDG_RUNTIME_DIR, another emptyDir that
# the compositor's container shares. GTK 3 would try X11 first without
# GDK_BACKEND.
#
# The image sets no LANG. The image holds no locale files, and with a
# LANG such as C.UTF-8, wxWidgets fails to set the locale and reports
# it in a modal dialog. A modal dialog ends PHD2 on a compositor with
# no seat.
ENV HOME=/home/phd2 \
    XDG_RUNTIME_DIR=/run/wayland \
    WAYLAND_DISPLAY=wayland-0 \
    GDK_BACKEND=wayland
USER 1000:1000
EXPOSE 4400
ENTRYPOINT ["/usr/bin/phd2-start"]
