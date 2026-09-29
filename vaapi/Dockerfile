# The vaapi image: the vulkan image plus the VA-API loader and the
# Intel media driver, for a program that decodes video on the node's
# GPU. ffmpeg builds on it.
#
# The builder installs from the same Debian image and the same snapshot
# date as the vulkan image, so each library that both trees hold is the
# same file, and the closure leaves it out of this layer.

FROM debian:trixie-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a AS closure
ARG VERSION
# snapshot.sh and closure.sh come from the vulkan directory, the build
# context named builder.
COPY --from=builder snapshot.sh snapshot.sha256 /
RUN sh /snapshot.sh "$VERSION" \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        libva2 \
        libva-drm2 \
        intel-media-va-driver \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder closure.sh /
COPY vaapi-closure.sh /
# The tree of the image under this one, so the closure can leave out
# every file that image already holds.
COPY --from=vulkan / /base/
RUN sh /vaapi-closure.sh /out /base

FROM vulkan
COPY --from=closure /out /
