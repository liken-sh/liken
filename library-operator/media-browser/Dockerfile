# syntax=docker/dockerfile:1

# The media browser's image: the Rust client, and the Wayland and Vulkan
# libraries it opens by name on a real machine. The build context is this
# directory, and brand/ and media-screen/ arrive as named build
# contexts for the two path crates below.

FROM rust:1.97.1-bookworm@sha256:0e2bcaef56d041a486784e54104a81aebe0da44bd03019bd70bc0401e42e4a97 AS build
WORKDIR /src

# The manifest, the lock file, and the toolchain pin come first, so a source
# edit reuses the cached dependency layer.
#
# The dependencies are almost all of the build, and they change only when the
# lock file changes.
COPY Cargo.toml Cargo.lock rust-toolchain.toml ./

# liken-iced is a path dependency in brand/, which the build takes as a
# second build context named brand and copies to /brand, the same place
# relative to /src that brand/ has relative to this directory. The stub
# build below needs only its manifest, so the manifest and its lock file
# arrive here and the crate's sources arrive after that layer. A change to
# the brand then recompiles this crate alone and leaves the third-party
# dependency layer cached.
COPY --from=brand iced/Cargo.toml iced/Cargo.lock /brand/iced/

# media-screen is a path dependency at the top of the repository, which the
# build takes as a third build context named media-screen and copies to
# /media-screen, the same way. The crate is a workspace of its own, so its
# manifest holds its dependency versions too.
COPY --from=media-screen Cargo.toml /media-screen/

# A stub library and a stub binary for this crate, and stub libraries for
# liken-iced and media-screen, give cargo something to compile the
# dependencies against. That build is the layer the image cache keeps.
RUN mkdir -p src /brand/iced/src /media-screen/src \
    && echo 'fn main() {}' > src/main.rs \
    && : > src/lib.rs \
    && : > /brand/iced/src/lib.rs \
    && : > /media-screen/src/lib.rs

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
COPY --from=brand iced/src/ /brand/iced/src/
COPY --from=brand liken.svg liken.css /brand/
COPY --from=brand fonts/ /brand/fonts/
COPY --from=media-screen src/ /media-screen/src/

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

# The touch marks the three crates' own sources newer than the stub build,
# so cargo compiles them again and leaves the dependencies as they are.
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    touch src/main.rs src/lib.rs /brand/iced/src/*.rs \
        /media-screen/src/lib.rs \
    && MEDIA_BROWSER_VERSION="$VERSION" cargo build --release --locked

# The runtime is liken's Vulkan base: the loader, the Intel and AMD
# drivers, and the Wayland and keyboard libraries the toolkit opens by
# name, on scratch. The compositor's image builds on the same Vulkan tree,
# so the client and the compositor run one Mesa, and a node that runs
# both holds the drivers once. The base carries the zoneinfo database
# too, because Rust's standard library has no time zones.
#
# The bake file at the top of the repository gives this build the vulkan
# image of the same commit as the named context vulkan.
FROM vulkan

COPY --from=build /src/target/release/media-browser /media-browser

# The binary is the container's own process, so the kubelet sends it the
# signal and carries its exit code out as the pod's outcome.
ENTRYPOINT ["/media-browser"]
