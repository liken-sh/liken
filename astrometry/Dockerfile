# The astrometry image: astrometry.net's astrometry-engine and
# solve-field, and SourceExtractor's source-extractor, with every
# library they load, on scratch. astrophotography-operator's worker
# image builds on it: the worker extracts stars beside the frames, and
# the solver solves the star lists against the index files.
#
# The image holds no index files. A person or the operator puts them in
# a volume, and the caller passes astrometry-engine a configuration that
# names them.
#
# The builder installs from the same Debian image and the same snapshot
# date as the other images on Debian. snapshot.sh and closure.sh come
# from the vulkan directory, the build context named builder.

FROM debian:trixie-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a AS closure
ARG VERSION
COPY --from=builder snapshot.sh snapshot.sha256 /
RUN sh /snapshot.sh "$VERSION" \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
        astrometry.net \
        source-extractor \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder closure.sh /
COPY astrometry-closure.sh /
RUN sh /astrometry-closure.sh /out

FROM scratch
COPY --from=closure /out /
