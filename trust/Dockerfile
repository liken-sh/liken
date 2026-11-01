# The trust image: the CA bundle at the path where OpenSSL, GnuTLS,
# libcurl, and Go read it, its notice, and its SBOM record. An image
# that verifies TLS copies all three:
#
#   COPY --from=trust / /
#
# The snapshot is the one that VERSION names, fetched by the checksum
# that curl.se publishes beside it.
FROM scratch AS fetch
ADD --checksum=sha256:796f1389bfea13c0ed78f663b765baaff54a508e5483786bd6ece73c5e3e3cbe \
    https://curl.se/ca/cacert-2026-10-20.pem /cacert.pem

# syft finds no package in a file that a build fetches, so the image
# carries a CycloneDX record of the bundle that image-sbom writes from
# pin.toml, and syft copies its component into the SBOM of this image
# and of every image that copies this one. The build context manifest
# is toolchains/manifest, the reader of the pin manifests.
FROM golang AS record
WORKDIR /src
COPY --from=manifest . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /image-sbom ./image-sbom
COPY pin.toml /pins/trust.toml
RUN /image-sbom -out /trust.cdx.json /pins/trust.toml

# --chmod applies to the directories that COPY creates as well, so the
# mode is symbolic: X sets execute on the directories alone.
FROM scratch
COPY --from=fetch --chmod=u=rwX,go=rX /cacert.pem /etc/ssl/certs/ca-certificates.crt
# Mozilla's certificate data is under the MPL 2.0, which requires that
# a copy of the data tell its recipient so and where the license is.
COPY --chmod=u=rwX,go=rX copyright /usr/share/doc/mozilla-ca/copyright
COPY --from=record --chmod=u=rwX,go=rX /trust.cdx.json /usr/share/doc/liken/trust.cdx.json
