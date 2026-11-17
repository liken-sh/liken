# transect's image: the server, the libraries that the loader resolves
# for it, the CA bundle, and the license notices, on scratch. The
# account of transect reads nearly every kind in the cluster, so the
# image carries no shell and no tools. The templates, the styles, the
# scripts, and the brand's fonts are compiled into the binary.
#
# The build context is transect/. The named build contexts bring the
# rest: brand, for the two fonts that the binary embeds and their
# license; toolchains, for the cargo-auditable that
# toolchains/cargo-auditable/fetch.sh pins; notices, for cargo.sh;
# builder, the vulkan directory, for snapshot.sh and closure.sh; and
# manifest, toolchains/manifest, for image-sbom. The bake file at the
# top of the repository gives this build the trust image of the same
# commit.

FROM rust AS build
WORKDIR /src

# cargo-auditable records the crate graph of the build in the binary's
# `.dep-v0` section, so syft and Trivy list the crates from the image.
# toolchains/cargo-auditable/fetch.sh pins the tool and says why. Every
# build below runs through it, the stub build too, so the dependencies
# compile once under the same wrapper that the real build uses.
COPY --from=toolchains pin.sh /toolchains/
COPY --from=toolchains cargo-auditable/fetch.sh /toolchains/cargo-auditable/
RUN bash /toolchains/cargo-auditable/fetch.sh /usr/local/cargo/bin

# The dependencies build in a layer of their own, against the
# workspace's manifests and an empty source for each crate, so a cached
# build reuses them until a manifest or Cargo.lock changes. This stage
# copies the whole context and keeps only the manifests and those
# stubs. It runs again on every source change, but its output is the
# same, and BuildKit keys the COPY below by the files' contents, so the
# layers after it stay cached. A crate added under crates/ needs no
# change here.
#
# The build leaves rust-toolchain.toml out. The rust image holds the
# rustc of the same version, which `ci pins` checks, and no clippy and
# no rustfmt, so with the file rustup would download those two
# components from the network on each build.
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

# The cache mounts hold the crate registry and the git checkouts between
# builds, so a rebuild on a workstation downloads no crate a second
# time. --locked builds the versions the lock file names and never
# rewrites it. .cargo/config.toml sets jemalloc's options, which the
# build of jemalloc compiles into it.
FROM build AS compile
COPY --from=skeleton /skeleton ./
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    cargo auditable build --locked --release \
    && rm -r crates

COPY crates crates
# web::assets embeds the two fonts from brand/ by a path that leaves
# the workspace, so they land at /brand, the same place relative to
# /src that brand/ has relative to transect/.
COPY --from=brand fonts/SourceSans3VF-Upright.otf.woff2 fonts/SourceSans3VF-Italic.otf.woff2 /brand/fonts/
COPY --from=notices cargo.sh /
# COPY keeps the files' times from the checkout, which can be older than
# the stubs' build, so the touch of each crate's root makes cargo
# compile transect's crates again and leave the dependencies as they
# are. Then cargo.sh copies the license files of the standard library
# and of each crate that the binary links, which notices/cargo.sh says
# the image needs. It runs here because the crate sources are in the
# registry cache, which only this RUN mounts.
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    find crates -maxdepth 3 \( -name lib.rs -o -name main.rs \) -exec touch {} + \
    && cargo auditable build --locked --release \
    && cp target/release/transect /transect \
    && sh /cargo.sh /notices -p transect

# The binary links glibc, so the image carries the libraries that the
# loader resolves for it, as a closure. The closure installs from the
# same Debian image and the same snapshot date as the base images.
# snapshot.sh and closure.sh, the closure library, come from the vulkan
# directory, the build context named builder, and SNAPSHOT is vulkan's
# version. The upgrade brings the image's own packages to the
# snapshot, so the libraries and their records come from that date.
FROM debian AS closure
ARG SNAPSHOT
COPY --from=builder snapshot.sh snapshot.sha256 closure.sh /
RUN sh /snapshot.sh "$SNAPSHOT" \
    && apt-get upgrade -y \
    && rm -rf /var/lib/apt/lists/*
COPY --from=compile /transect /transect
# The closure script sources closure.sh, which also writes the
# copyright file of each Debian package that the closure took files
# from, the list of those packages and their sources, and the dpkg
# record of each package that syft and Trivy read.
COPY transect-closure.sh /
RUN sh /transect-closure.sh /out

# syft finds no package in the two htmx files that the binary embeds,
# so the image carries a CycloneDX record of both that image-sbom
# writes from their pin.toml, and syft copies its components into the
# image's SBOM. The build context manifest is toolchains/manifest, the
# reader of the pin manifests.
FROM golang AS record
WORKDIR /src
COPY --from=manifest . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /image-sbom ./image-sbom
COPY crates/transect/src/web/static/pin.toml /pins/htmx.toml
RUN /image-sbom -out /transect.cdx.json /pins/htmx.toml

FROM scratch
COPY --from=closure /out /
# The registry, Prometheus, and log store clients verify TLS against
# the bundle that the trust component pins for every image of liken,
# with its notice. The API server's CA comes from the ServiceAccount
# mount.
COPY --from=trust / /
COPY --from=compile /notices /
# The binary embeds the brand's two faces, and their license, the SIL
# Open Font License, asks that it travel with them. htmx and its SSE
# extension are under the Zero-Clause BSD license, which asks for
# nothing, and their license files travel with them all the same.
COPY --from=brand fonts/LICENSE.md /usr/share/doc/source-sans-3/LICENSE.md
COPY crates/transect/src/web/static/htmx.LICENSE /usr/share/doc/htmx.org/LICENSE
COPY crates/transect/src/web/static/sse.LICENSE /usr/share/doc/htmx-ext-sse/LICENSE
COPY --from=record /transect.cdx.json /usr/share/doc/liken/transect.cdx.json
# deploy/transect.yaml runs the pod as 65534, and `docker run` runs the
# binary as the same user.
USER 65534:65534
EXPOSE 8080
ENV TRANSECT_LISTEN=0.0.0.0:8080
ENTRYPOINT ["/transect"]
