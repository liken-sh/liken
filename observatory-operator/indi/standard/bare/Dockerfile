# The bare drivers, built against the libindi of the indi images. The
# build installs from the same dated snapshots as indi/Dockerfile, with
# the same keys and the same CA bundle, so the captures describe the
# libindi that the images ship. VERSION is the date in
# indi/package.toml, and the make target passes it.
#
# The image runs on a workstation only, to capture the drivers, and is
# never published. It keeps the compiler and the headers, because it
# is thrown away after the capture.
FROM ubuntu
ARG VERSION
COPY --from=trust / /
COPY --from=indi mutlaqja.asc pch.asc /etc/apt/trusted.gpg.d/
COPY --from=indi snapshot.sh /snapshot.sh
# libindi-dev does not depend on the headers that its own headers
# include: libastro.h includes libnova, and indiccdchip.h includes
# fitsio.h. /etc/indi-versions records the package the drivers link,
# as it does in the indi images, and the capture reads it from there.
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
