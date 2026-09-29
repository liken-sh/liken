# The vulkan image: the Vulkan loader, the Intel and AMD drivers, and
# the client libraries a Wayland program opens, on nothing else. It is
# the lowest base. vaapi and weston build on it, and every Vulkan client
# liken ships builds on it or on an image above it.
#
# The image is a library closure on scratch: vulkan-closure.sh collects
# the seeds it names, every library the loader resolves for them, and
# the data files they read, and nothing else. closure.sh holds the
# functions that every base's closure script uses, and snapshot.sh
# points apt at one day of snapshot.debian.org. The other bases take
# this directory as the named build context builder for those two
# files.

# The builder is one Debian image by its digest, and apt installs from
# the snapshot of one date, the version in package.toml. So the build
# installs the same packages every time, and a new date or a new digest
# is a change to the recipe, which needs a new revision. The upgrade
# brings the packages of the image itself to the same date.
FROM debian:trixie-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a AS closure
ARG VERSION
COPY snapshot.sh /
# mesa-vulkan-drivers and libvulkan1 are the drivers and the loader.
# libgl1-mesa-dri ships drirc, the per-application workarounds that the
# drivers read; the tree takes that one file from it. libxkbcommon0 and
# tzdata carry the keymap data and the zoneinfo that the clients read.
RUN sh /snapshot.sh "$VERSION" \
    && apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        mesa-vulkan-drivers \
        libvulkan1 \
        libgl1-mesa-dri \
        libxkbcommon0 \
        tzdata \
    && rm -rf /var/lib/apt/lists/*
COPY closure.sh vulkan-closure.sh /
RUN sh /vulkan-closure.sh /out

FROM scratch
COPY --from=closure /out /
