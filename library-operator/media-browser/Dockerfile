# syntax=docker/dockerfile:1

# The media browser's image: the Rust client, and the Wayland and Vulkan
# libraries it opens by name on a real machine. The build context is this
# directory alone, so the crate is the only source the image sees.

# The runtime base of the last stage. An argument a FROM line reads has
# to be declared before the first stage, so it is here and not beside
# the stage that uses it.
ARG VULKAN_BASE=ghcr.io/liken-sh/vulkan:2026.09.08-001

FROM rust:1.97.1-bookworm@sha256:0e2bcaef56d041a486784e54104a81aebe0da44bd03019bd70bc0401e42e4a97 AS build
WORKDIR /src

# The manifest, the lock file, and the toolchain pin come first, so a source
# edit reuses the cached dependency layer.
#
# The dependencies are almost all of the build, and they change only when the
# lock file changes.
COPY Cargo.toml Cargo.lock rust-toolchain.toml ./

# liken-iced is a path dependency on the brand repository, a submodule inside
# this build context. The stub build below needs only its manifest, so the
# manifest and its lock file arrive here and the crate's sources arrive after
# that layer. A submodule bump then recompiles this crate alone and leaves the
# third-party dependency layer cached.
COPY brand/iced/Cargo.toml brand/iced/Cargo.lock ./brand/iced/

# A stub library and a stub binary for this crate, and a stub library for
# liken-iced, give cargo something to compile the dependencies against. That
# build is the layer the image cache keeps.
RUN mkdir src brand/iced/src \
    && echo 'fn main() {}' > src/main.rs \
    && : > src/lib.rs \
    && : > brand/iced/src/lib.rs

# The cache mounts hold the crate registry and the git checkouts between
# builds, so a rebuild on a workstation downloads no crate a second time.
# --locked builds the versions the lock file names and never rewrites it.
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    cargo build --release --locked

COPY src/ ./src/
# The ratings marks are baked into the binary from this directory.
COPY assets/ ./assets/
# liken-iced's sources land here, over the stub, with the two brand files it
# parses and the two font files it embeds. All four are inputs of the real
# build alone; the stub compiles without them.
COPY brand/iced/src/ ./brand/iced/src/
COPY brand/liken.svg brand/liken.css ./brand/
COPY brand/fonts/ ./brand/fonts/

# The release this build makes, for the `liken_build_info` gauge milestone
# 65 puts on every process. release.yaml names a version for the image tag
# already; a caller that also passes it here through --build-arg is what
# makes the gauge read the same string. A build with no argument, such as
# a workstation's `docker build`, compiles the binary reporting "dev".
#
# The argument is declared here, not at the top of the file, so setting it
# never invalidates the dependency layer above: only this RUN's cache
# entry depends on its value, and that layer is rebuilt on every source
# change regardless.
ARG VERSION=dev

# The touch marks both crates' own sources newer than the stub build, so cargo
# compiles them again and leaves the dependencies as they are.
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    touch src/main.rs src/lib.rs brand/iced/src/*.rs \
    && MEDIA_BROWSER_VERSION="$VERSION" cargo build --release --locked

# The runtime is liken's Vulkan base: the loader, the Intel and AMD
# drivers, and the Wayland and keyboard libraries the toolkit opens by
# name, on scratch. The display operator builds it beside the compositor,
# so the client and the compositor run one Mesa, and a node that runs
# both holds the drivers once. The base carries the zoneinfo database
# too, because Rust's standard library has no time zones.
#
# The tag pins the base to one release of the display operator. The
# argument is declared at the top of the file, and a workstation may set
# it to a base it built itself.
FROM ${VULKAN_BASE}

COPY --from=build /src/target/release/media-browser /media-browser

# The binary is the container's own process, so the kubelet sends it the
# signal and carries its exit code out as the pod's outcome.
ENTRYPOINT ["/media-browser"]
