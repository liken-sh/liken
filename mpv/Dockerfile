# The mpv image: the ffmpeg image plus mpv and every library and data
# file it opens by name. The ffmpeg image under it already holds the
# libav libraries that mpv links and the VA-API driver it decodes
# with. media-operator's player builds on it.
#
# Every stage below installs from the same Debian image and the same
# snapshot date as the images under this one, so each library that the
# trees share is the same file, and the closure leaves it out of this
# layer. snapshot.sh and closure.sh come from the vulkan directory, the
# build context named builder.
#
# mpv itself builds from the upstream release, because a fix that
# liken needs is in no package that sid has. Before 0.42.0, mpv did not
# reset FFmpeg's TrueHD framer on a seek, so a TrueHD track that the
# player passed through to a receiver went silent after each seek.
# Every library mpv builds against still comes from the snapshot.

# The release tarball, pinned by its sha256. BuildKit checks the
# checksum when it fetches the file, so no CA bundle enters the build.
# The tarball is the archive that GitHub serves for the release's tag.
# latest.sh writes the URL and the checksum here and in pin.toml, and
# it checks the release manager's signature of the release before it
# writes them.
ARG MPV_VERSION=0.42.0
ARG MPV_URL=https://github.com/mpv-player/mpv/archive/refs/tags/v0.42.0.tar.gz
ARG MPV_SHA256=bcfebbfa2b65a6d88dc97ea18117831072373e38b5785a2afb874ed4df5070dd

# mpv, built with meson into /usr, which the closure stage below
# collects from.
#
# The build turns on what the player uses and nothing else. The
# player runs mpv with --vo=dmabuf-wayland, --hwdec=vaapi, and
# --ao=pipewire (playerArgv in media-operator/player.go), so the build
# keeps Wayland, the dmabuf-wayland output, and VA-API through Wayland
# and through a DRM node. The DRM option also builds libdrm's format
# tables that the dmabuf path reads. libass draws the subtitles and the
# OSD, and libplacebo is a dependency that mpv always links. zlib
# reads the compressed tracks of a Matroska file. iconv is glibc's.
#
# Lua stays on, because the player passes --osc=no and --load-console=no,
# and mpv defines those two options only in a build with Lua. A build
# without Lua refuses them and does not start. Debian's package links
# Lua 5.2, and this build links the same library.
#
# Every other option is off. No liken image has an X11 server, a
# PulseAudio or JACK server, or an ALSA device that mpv opens directly,
# and no item is a disc, a capture device, an archive, or a lavfi
# graph: media-operator resolves only https://, nfs://, claim://, and
# pattern:// items, and a pattern is a file in the player image. The
# OpenGL and Vulkan outputs cannot draw in a pod, because the EGL
# stack is in the compositor's image and not in this one. lcms2 serves
# only those outputs. uchardet only guesses the charset of a subtitle
# file, and the image carries none of glibc's gconv modules, so mpv
# could not convert a file in the charset that uchardet names.
# FFmpeg reads the https:// items, and with --tls-verify=yes it checks
# each certificate against the bundle that the player image copies
# from the trust component. 0.42.0's libcurl option would replace
# FFmpeg as that reader, so the build turns it off.
#
# --auto-features=disabled turns off each option that the list does
# not name, such as the macOS, Windows, and Android options, and each
# option that a later release adds. The list names every option of
# this release that applies to Linux, so a reader sees each choice.
# The options that 0.42.0 adds are off: DVD-Audio (dvda), AMD's AMF
# (amf), and subrandr. subrandr is a library of its own that sid does
# not package, and mpv 0.42.0 gives it only YouTube's SRV3 subtitles
# (the subrandr/srv3 codec in sub/sd_sbr.c). WebVTT still renders
# through libass.
#
# The build keeps mpv's default GPL-2.0-or-later license. -Dgpl=false
# would remove only options that are already off, and mpv's Copyright
# file advises against an LGPL build of the player program.
#
# dpkg-buildflags gives the same hardening flags as Debian's build of
# the same release, and the install strips the binary as Debian's
# packages are stripped.
FROM debian AS build
ARG VERSION
ARG MPV_VERSION
ARG MPV_SHA256
ARG MPV_URL
COPY --from=builder snapshot.sh snapshot.sha256 /
RUN sh /snapshot.sh "$VERSION" \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        gcc libc6-dev pkgconf meson ninja-build dpkg-dev python3 patch \
        libavcodec-dev libavfilter-dev libavformat-dev libavutil-dev \
        libswresample-dev libswscale-dev libplacebo-dev libass-dev \
        libwayland-dev libwayland-bin wayland-protocols libxkbcommon-dev \
        libdrm-dev libdisplay-info-dev libva-dev \
        libpipewire-0.3-dev liblua5.2-dev zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*
ADD --checksum=sha256:${MPV_SHA256} ${MPV_URL} /mpv.tar.gz
# patches/ holds the upstream commits that the build applies to the
# release, each one the output of git format-patch for the commit, with
# its author, date, and id. pin.toml names the same commits as
# backports, so the image's SBOM states them. The directory can be
# empty. patch stops the build when a patch does not apply exactly. The
# grep stops it when the tree that the build compiles has no reset of
# the spdif muxer, the fix for the silent TrueHD passthrough after a
# seek.
COPY patches/ /patches/
RUN mkdir /src && tar -C /src --strip-components=1 -xzf /mpv.tar.gz \
    && cd /src \
    && for patch in /patches/*.patch; do \
         [ -e "$patch" ] || continue; \
         patch -p1 --forward --batch --fuzz=0 < "$patch" || exit 1; \
       done \
    && grep -q '\.reset = ad_spdif_reset' audio/decode/ad_spdif.c \
    && export DEB_BUILD_MAINT_OPTIONS=hardening=+all \
    && CFLAGS="$(dpkg-buildflags --get CFLAGS) $(dpkg-buildflags --get CPPFLAGS)" \
       LDFLAGS="$(dpkg-buildflags --get LDFLAGS)" \
       meson setup build \
        --prefix=/usr --libdir=lib/x86_64-linux-gnu \
        --buildtype=plain --wrap-mode=nodownload --auto-features=disabled \
        -Dgpl=true -Dcplayer=true -Dlibmpv=false -Dbuild-date=false \
        -Dtests=false -Dfuzzers=false \
        -Dwayland=enabled -Ddmabuf-wayland=enabled -Ddrm=enabled \
        -Dvaapi=enabled -Dvaapi-wayland=enabled -Dvaapi-drm=enabled \
        -Dvaapi-x11=disabled -Dvdpau=disabled -Dvdpau-gl-x11=disabled \
        -Dcuda-hwaccel=disabled -Dcuda-interop=disabled \
        -Dgl=disabled -Dplain-gl=disabled -Degl=disabled -Degl-drm=disabled \
        -Degl-wayland=disabled -Degl-x11=disabled -Dgl-x11=disabled -Dgbm=disabled \
        -Dvulkan=disabled -Dshaderc=disabled -Dspirv-cross=disabled \
        -Dx11=disabled -Dxv=disabled -Dx11-clipboard=disabled \
        -Dcaca=disabled -Dsixel=disabled -Dsdl2-video=disabled \
        -Dpipewire=enabled -Dalsa=disabled -Dpulse=disabled -Djack=disabled \
        -Doss-audio=disabled -Dsndio=disabled -Dopenal=disabled -Dsdl2-audio=disabled \
        -Dlua=lua5.2 -Djavascript=disabled -Dcplugins=disabled \
        -Diconv=enabled -Dzlib=enabled -Dvector=enabled \
        -Duchardet=disabled -Dlcms2=disabled -Djpeg=disabled -Dzimg=disabled \
        -Drubberband=disabled -Dvapoursynth=disabled \
        -Dlibavdevice=disabled -Dlibarchive=disabled \
        -Dlibbluray=disabled -Dcdda=disabled -Ddvdnav=disabled \
        -Ddvbin=disabled -Ddvda=disabled -Dsdl2-gamepad=disabled \
        -Damf=disabled -Dlibcurl=disabled -Dsubrandr=disabled \
        -Dmanpage-build=disabled -Dhtml-build=disabled -Dpdf-build=disabled \
    && meson compile -C build \
    && meson install -C build --strip \
    && install -D -m 644 -t /usr/share/doc/mpv Copyright LICENSE.GPL LICENSE.LGPL \
    && /usr/bin/mpv --no-config --version | grep -q "^mpv v${MPV_VERSION} "

# The closure starts from the build stage, so every library that mpv
# links is already installed, and it adds the packages of the files
# that mpv opens by name. libspa-0.2-modules carries the SPA plugins
# that mpv's PipeWire output loads by name. pipewire-bin is here for
# one file, /usr/share/pipewire/client.conf. fontconfig-config carries
# the configuration that libass reads.
FROM build AS closure
ARG VERSION
RUN sh /snapshot.sh "$VERSION" \
    && apt-get install -y --no-install-recommends \
        libpipewire-0.3-0t64 \
        libspa-0.2-modules \
        pipewire-bin \
        fontconfig-config \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder closure.sh /
COPY mpv-closure.sh /
# The tree of the images under this one, ffmpeg on vaapi on vulkan, so
# the closure can leave out every file they already hold.
COPY --from=ffmpeg / /base/
RUN sh /mpv-closure.sh /out /base

# syft finds mpv in the image but no package for it, because mpv builds
# from its tarball and no dpkg record names it. So the image carries a
# CycloneDX record of the tarball that image-sbom writes from pin.toml,
# and syft copies its component, with NVD's CPE for mpv, into the SBOM
# of this image and of each image that builds on it. The build context
# manifest is toolchains/manifest, the reader of the pin manifests.
FROM golang AS record
WORKDIR /src
COPY --from=manifest . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /image-sbom ./image-sbom
COPY pin.toml /pins/mpv.toml
RUN /image-sbom -out /mpv.cdx.json /pins/mpv.toml

# The entrypoint is mpv, so a release can run it and read what it says.
FROM ffmpeg
COPY --from=closure /out /
COPY --from=record /mpv.cdx.json /usr/share/doc/liken/mpv.cdx.json
ENTRYPOINT ["/usr/bin/mpv"]
