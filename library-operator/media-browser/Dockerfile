# syntax=docker/dockerfile:1

# The media browser's image: the Rust client, and the Wayland and Vulkan
# libraries it opens by name on a real machine. The build context is this
# directory alone, so the crate is the only source the image sees.

FROM rust:1.97.1-bookworm@sha256:0e2bcaef56d041a486784e54104a81aebe0da44bd03019bd70bc0401e42e4a97 AS build
WORKDIR /src

# The manifest, the lock file, and the toolchain pin come first, so a source
# edit reuses the cached dependency layer.
#
# The dependencies are almost all of the build, and they change only when the
# lock file changes.
COPY Cargo.toml Cargo.lock rust-toolchain.toml ./

# liken-iced is a path dependency on the brand repository, a submodule inside
# this build context. cargo reads its manifest and its sources to resolve the
# dependency, so the crate arrives before the stub build below and shares that
# cached layer. It changes only when the submodule pin moves.
COPY brand/iced/ ./brand/iced/
COPY brand/liken.svg brand/liken.css ./brand/

# A stub library and a stub binary give cargo something to compile the
# dependencies against. That build is the layer the image cache keeps.
RUN mkdir src \
    && echo 'fn main() {}' > src/main.rs \
    && : > src/lib.rs

# The cache mounts hold the crate registry and the git checkouts between
# builds, so a rebuild on a workstation downloads no crate a second time.
# --locked builds the versions the lock file names and never rewrites it.
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    cargo build --release --locked

COPY src/ ./src/

# The touch marks the crate's own sources newer than the stub build, so cargo
# compiles this crate again and leaves the dependencies as they are.
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    touch src/main.rs src/lib.rs \
    && cargo build --release --locked

# A distribution base, unlike the operator's scratch. The toolkit opens a wide
# dynamic closure at run time, the Wayland client libraries and the Vulkan
# loader with its driver, and the closure comes soundest from the distribution
# that built them.
#
# The digest pins the base to one exact image, so a rebuild starts from the same
# bytes after Ubuntu republishes the 26.04 tag. To move to a newer base, read
# the new digest with docker inspect and replace it here.
FROM ubuntu:26.04@sha256:2260313b31c8c011cd2eebe728008efac1b3982be73eb71348ea2648d2c0e09b

# libwayland-client0 and libxkbcommon0 are the Wayland and keyboard libraries
# winit opens by name. libvulkan1 with mesa-vulkan-drivers is the renderer's
# path to the Intel graphics these machines have.
#
# fonts-adobe-sourcesans3 carries the face the display draws its text in.
# tzdata carries the zoneinfo database, because Rust's standard library has
# no time zones and the clock reads the zone the environment names.
#
# The Snapshot line pins apt to the archive as it stood at one instant, so the
# versions below stay fetchable after Ubuntu publishes newer ones. To move the
# image forward, set a later serial and refresh the six versions from a build at
# that serial. snapshot.ubuntu.com serves over HTTPS, and the base carries no
# certificate store, so the build installs ca-certificates from the default
# archive first, then purges it after the pinned install so it leaves no trace
# in the runtime.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && sed -i '/^Signed-By:/a Snapshot: 20260822T000000Z' /etc/apt/sources.list.d/ubuntu.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        libwayland-client0=1.24.0-2 \
        libxkbcommon0=1.13.1-1 \
        libvulkan1=1.4.341.0-1 \
        mesa-vulkan-drivers=26.0.8-1ubuntu0.3 \
        fonts-adobe-sourcesans3=3.052+dfsg-2 \
        tzdata=2026c-0ubuntu0.26.04.1 \
    && apt-get purge -y ca-certificates \
    && apt-get autoremove -y --purge \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /src/target/release/media-browser /media-browser

# The binary is the container's own process, so the kubelet sends it the
# signal and carries its exit code out as the pod's outcome.
ENTRYPOINT ["/media-browser"]
