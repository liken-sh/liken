# The ffmpeg image: the vaapi image plus ffmpeg and ffprobe and every
# library they load. mpv builds on it, and so do the images that run
# ffmpeg: display-operator's capture container, media-operator's API,
# and library-operator's file facts.
#
# The builder installs from the same Debian image and the same snapshot
# date as the images under this one, so each library that the trees
# share is the same file, and the closure leaves it out of this layer.

FROM debian:trixie-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a AS closure
ARG VERSION
# snapshot.sh and closure.sh come from the vulkan directory, the build
# context named builder.
COPY --from=builder snapshot.sh /
RUN sh /snapshot.sh "$VERSION" \
    && apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        ffmpeg \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder closure.sh /
COPY ffmpeg-closure.sh /
# The tree of the images under this one, vaapi on vulkan, so the
# closure can leave out every file they already hold.
COPY --from=vaapi / /base/
RUN sh /ffmpeg-closure.sh /out /base

# The entrypoint is ffmpeg, so a release can run it and read what it
# says.
FROM vaapi
COPY --from=closure /out /
ENTRYPOINT ["/usr/bin/ffmpeg"]
