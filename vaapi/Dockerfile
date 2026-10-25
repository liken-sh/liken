# The vaapi image: the vulkan image plus the VA-API loader, the Intel
# media driver, and Mesa's VA driver for AMD, for a program that decodes
# video on the node's GPU. ffmpeg builds on it.
#
# The Intel driver is Debian's non-free build, from the non-free
# component. Debian's free build leaves out every codec whose driver
# path runs Intel's prebuilt GPU kernels, and on Gen12 GPUs, Tiger Lake
# and Alder Lake among them, that includes AV1 decode. The kernels are
# MIT-licensed binaries without their source, which is why Debian
# files the package as non-free. libva opens the driver at run time,
# and no program in this repository links it.
#
# The builder installs from the same Debian image and the same snapshot
# date as the vulkan image, so each library that both trees hold is the
# same file, and the closure leaves it out of this layer.

FROM debian AS closure
ARG VERSION
# snapshot.sh and closure.sh come from the vulkan directory, the build
# context named builder.
COPY --from=builder snapshot.sh snapshot.sha256 /
RUN sh /snapshot.sh "$VERSION" non-free \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        libva2 \
        libva-drm2 \
        intel-media-va-driver-non-free \
        mesa-va-drivers \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder closure.sh /
COPY vaapi-closure.sh /
# The tree of the image under this one, so the closure can leave out
# every file that image already holds.
COPY --from=vulkan / /base/
RUN sh /vaapi-closure.sh /out /base

FROM vulkan
COPY --from=closure /out /
