# syntax=docker/dockerfile:1

# The Corrosion sidecar's image. A Corrosion agent runs beside every
# scanner and every media browser and replicates the catalog to them.
# The project builds it from one pinned upstream commit, so every
# agent in a cluster runs the same bytes, and a move to a newer
# commit is a change to this file that a release carries.
FROM rust:1.89-bookworm@sha256:948f9b08a66e7fe01b03a98ef1c7568292e07ec2e4fe90d88c07bb14563c84ff AS build

WORKDIR /usr/src/corrosion

# The base carries the toolchain Corrosion's own rust-toolchain.toml
# names, so no toolchain installs at build time, and it carries git
# and the C toolchain. Corrosion's .cargo/config.toml links with
# clang and mold, so those two packages are the whole of what the
# build adds.
RUN apt-get update \
    && apt-get install -y --no-install-recommends clang mold \
    && rm -rf /var/lib/apt/lists/*

RUN git init . \
    && git remote add origin https://github.com/superfly/corrosion.git \
    && git fetch --depth 1 origin 936de111d223a5e80d476bd1195e21b3eaca9762 \
    && git checkout FETCH_HEAD

# The project's patches to Corrosion apply here, between the checkout
# and the build. There are none, so there is no patches/ directory.

# The cache mounts keep the cargo registry and the git checkouts
# between builds, so only a change to the lock file downloads again.
#
# Upstream's release profile keeps debug information, which makes the
# binary 314 MiB. The environment variable overrides that one profile
# setting, so the image carries the stripped binary and no patch to
# upstream.
RUN --mount=type=cache,target=/usr/local/cargo/registry --mount=type=cache,target=/usr/local/cargo/git \
    CARGO_PROFILE_RELEASE_STRIP=true cargo build --release --locked --bin corrosion

FROM gcr.io/distroless/cc-debian12@sha256:e5d81ddde149641e2a9ba55be4545bc125c67de07508b03ba4c22e6eb0ded5aa

# The runtime is distroless: a libc and nothing else, no shell and no
# tools, because the sidecar holds the catalog and answers on the
# pod's network.
COPY --from=build /usr/src/corrosion/target/release/corrosion /corrosion

# The configuration and the schema arrive in the image because the pod
# has no shell to render a file and no `ConfigMap` for the sidecar. The
# schema directory holds `catalog.sql`, so every agent this image
# starts loads the same catalog schema.
COPY config.toml /etc/corrosion/config.toml
COPY schema/ /etc/corrosion/schema/

# The entrypoint is the bare binary, so `corrosion cluster members` and
# the other commands still run from this image with their own
# arguments. The default command is the agent, so a pod that names
# neither a command nor arguments runs the agent on the configuration
# above.
ENTRYPOINT ["/corrosion"]
CMD ["agent", "--config", "/etc/corrosion/config.toml"]
