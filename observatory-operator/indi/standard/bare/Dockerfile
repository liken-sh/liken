# The bare drivers, built against the same libindi as the indi images.
# The build installs from the same dated package snapshots as
# indi/Dockerfile, with the same keys, CA bundle, and InRelease digests,
# so the captures describe the libindi that the indi images ship.
# snapshot.sh reads the digests from snapshot.sha256. VERSION is the
# date in indi/package.toml, and the make target passes it.
#
# This image runs only on a workstation, to capture the drivers, and is
# never published. It keeps the compiler and the headers because it is
# deleted after the capture.
FROM ubuntu
ARG VERSION
COPY --from=trust / /
COPY --from=indi mutlaqja.asc pch.asc /etc/apt/trusted.gpg.d/
COPY --from=indi snapshot.sh snapshot.sha256 /
# libindi-dev does not depend on the packages that provide the headers
# its own headers include: libastro.h includes libnova headers, and
# indiccdchip.h includes fitsio.h. /etc/indi-versions records the
# version of the package the drivers link against, as it does in the
# indi images, and the capture reads it from there.
RUN sh /snapshot.sh "$VERSION" \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        indi-bin \
        libindi-dev \
        libnova-dev \
        libcfitsio-dev \
        zlib1g-dev \
        g++ \
        cmake \
        make \
        pkg-config \
    && rm -rf /var/lib/apt/lists/* \
    && dpkg-query -W indi-bin > /etc/indi-versions
COPY . /src
RUN cmake -S /src -B /build && cmake --build /build -j 4 && cmake --install /build --prefix /usr
ENTRYPOINT ["/usr/bin/indiserver"]
