# The transect image: the server binary, the shared libraries it loads,
# the CA bundle, and the license notices, on scratch. transect's
# ServiceAccount can read nearly every kind in the cluster, so the image
# has no shell and no tools. The templates, styles, scripts, and fonts
# are compiled into the binary.
#
# The build context is transect/. The named build contexts supply the
# rest:
#   brand       the two fonts that the binary embeds, and their license
#   toolchains  the cargo-auditable pin (cargo-auditable/fetch.sh)
#   notices     cargo.sh
#   builder     the vulkan directory, for snapshot.sh and closure.sh
#   manifest    toolchains/manifest, for image-sbom
# docker-bake.hcl at the top of the repository also passes the trust
# image of the same commit as the build context trust.

FROM rust AS build
WORKDIR /src

# cargo-auditable records the crate graph in the binary's `.dep-v0`
# section, so syft and Trivy can list the crates from the image. Every
# build below runs through it, including the dependency-only build, so
# the dependencies compile once and the final build reuses them.
COPY --from=toolchains pin.sh /toolchains/
COPY --from=toolchains cargo-auditable/fetch.sh /toolchains/cargo-auditable/
RUN bash /toolchains/cargo-auditable/fetch.sh /usr/local/cargo/bin

# The dependencies build in their own layer, from the workspace's
# manifests and an empty source file for each crate, so that layer stays
# cached until a manifest or Cargo.lock changes. This stage copies the
# whole context, so it runs again on every source change, but its output
# stays the same, and BuildKit caches the COPY below by file contents.
# A crate added under crates/ needs no change here.
#
# The skeleton leaves out rust-toolchain.toml. The rust image already
# has the rustc version that the file names (`ci pins` checks this), but
# not the clippy, llvm-tools, and rustfmt components that the file
# lists, so with the file rustup would download them on every build.
FROM rust AS skeleton
WORKDIR /context
COPY . .
RUN mkdir /skeleton \
    && cp -r --parents Cargo.toml Cargo.lock .cargo/config.toml /skeleton \
    && for crate in crates/*/; do \
         mkdir -p "/skeleton/${crate}src" \
         && cp "${crate}Cargo.toml" "/skeleton/$crate" \
         && if [ -f "${crate}src/main.rs" ]; then \
              echo 'fn main() {}' > "/skeleton/${crate}src/main.rs"; \
            else \
              touch "/skeleton/${crate}src/lib.rs"; \
            fi; \
       done

# The cache mounts keep the crate registry and git checkouts between
# builds, so a rebuild downloads no crate again. --locked builds the
# versions in Cargo.lock and never rewrites it. .cargo/config.toml sets
# jemalloc's options, which jemalloc's build compiles in, so the
# skeleton includes it.
FROM build AS compile
COPY --from=skeleton /skeleton ./
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    cargo auditable build --locked --release \
    && rm -r crates

COPY crates crates
# web::assets embeds the fonts by a path outside the workspace, so they
# go to /brand, which is at the same position relative to /src that
# brand/ has relative to transect/.
COPY --from=brand fonts/SourceSans3VF-Upright.otf.woff2 fonts/SourceSans3VF-Italic.otf.woff2 /brand/fonts/
COPY --from=notices cargo.sh /
# COPY keeps the checkout's file times, which can be older than the
# build of the empty sources, so cargo could take that build as up to
# date. The touch makes cargo compile transect's crates again and leaves
# the dependencies alone. cargo.sh copies the license files of the
# standard library and of each linked crate, and runs in this RUN
# because it reads the crate sources from the registry cache that this
# RUN mounts.
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    find crates -maxdepth 3 \( -name lib.rs -o -name main.rs \) -exec touch {} + \
    && cargo auditable build --locked --release \
    && cp target/release/transect /transect \
    && sh /cargo.sh /notices -p transect

# The binary links glibc dynamically, so the image needs the shared
# libraries it loads. The closure stage collects them from the same
# Debian image and snapshot date as the base images. snapshot.sh points
# apt at the snapshot of the date in SNAPSHOT, the vulkan base's
# version, and `apt-get upgrade` moves the builder's own packages to
# that snapshot, so the libraries and their dpkg records come from it.
FROM debian AS closure
ARG SNAPSHOT
COPY --from=builder snapshot.sh snapshot.sha256 closure.sh /
RUN sh /snapshot.sh "$SNAPSHOT" \
    && apt-get upgrade -y \
    && rm -rf /var/lib/apt/lists/*
COPY --from=compile /transect /transect
# transect-closure.sh sources closure.sh, which also writes each
# package's copyright file, the list of the packages and their sources,
# and the dpkg records that syft and Trivy read.
COPY transect-closure.sh /
RUN sh /transect-closure.sh /out

# syft finds no package in the htmx files that the binary embeds.
# image-sbom writes a CycloneDX record of them from their pin.toml, and
# syft copies its components into the image's SBOM.
FROM golang AS record
WORKDIR /src
COPY --from=manifest . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /image-sbom ./image-sbom
COPY crates/transect/src/web/static/pin.toml /pins/htmx.toml
RUN /image-sbom -out /transect.cdx.json /pins/htmx.toml

FROM scratch
COPY --from=closure /out /
# The registry, Prometheus, and log store clients verify TLS against
# the CA bundle of the trust component, which every `liken` image uses,
# and its license notice. The API server's CA comes from the
# ServiceAccount mount.
COPY --from=trust / /
COPY --from=compile /notices /
# The SIL Open Font License requires that the license travel with the
# embedded fonts. htmx and its SSE extension use the Zero-Clause BSD
# license, which does not require it, and the image includes their
# license files too.
COPY --from=brand fonts/LICENSE.md /usr/share/doc/source-sans-3/LICENSE.md
COPY crates/transect/src/web/static/htmx.LICENSE /usr/share/doc/htmx.org/LICENSE
COPY crates/transect/src/web/static/sse.LICENSE /usr/share/doc/htmx-ext-sse/LICENSE
COPY --from=record /transect.cdx.json /usr/share/doc/liken/transect.cdx.json
# deploy/transect.yaml also runs the pod as 65534, so `docker run`
# behaves the same way.
USER 65534:65534
EXPOSE 8080
ENV TRANSECT_LISTEN=0.0.0.0:8080
ENTRYPOINT ["/transect"]
