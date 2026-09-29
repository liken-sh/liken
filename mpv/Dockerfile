# The mpv image: the ffmpeg image plus mpv and every library and data
# file it opens by name. The ffmpeg image under it already holds the
# libav libraries that mpv links and the VA-API driver it decodes
# with. media-operator's player builds on it.
#
# The builder installs from the same Debian image and the same snapshot
# date as the images under this one, so each library that the trees
# share is the same file, and the closure leaves it out of this layer.

FROM debian:trixie-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a AS closure
ARG VERSION
# snapshot.sh and closure.sh come from the vulkan directory, the build
# context named builder.
COPY --from=builder snapshot.sh snapshot.sha256 /
# libspa-0.2-modules carries the SPA plugins that mpv's PipeWire output
# loads by name. pipewire-bin is here for one file,
# /usr/share/pipewire/client.conf. fontconfig-config carries the
# configuration that libass reads.
RUN sh /snapshot.sh "$VERSION" \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        mpv \
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

# The entrypoint is mpv, so a release can run it and read what it says.
FROM ffmpeg
COPY --from=closure /out /
ENTRYPOINT ["/usr/bin/mpv"]
