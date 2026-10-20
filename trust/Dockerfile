# The trust image: the CA bundle at the path where OpenSSL, GnuTLS,
# libcurl, and Go read it, and its notice. An image that verifies TLS
# copies both:
#
#   COPY --from=trust / /
#
# The snapshot is the one that VERSION names, fetched by the checksum
# that curl.se publishes beside it.
FROM scratch AS fetch
ADD --checksum=sha256:796f1389bfea13c0ed78f663b765baaff54a508e5483786bd6ece73c5e3e3cbe \
    https://curl.se/ca/cacert-2026-10-20.pem /cacert.pem

# --chmod applies to the directories that COPY creates as well, so the
# mode is symbolic: X sets execute on the directories alone.
FROM scratch
COPY --from=fetch --chmod=u=rwX,go=rX /cacert.pem /etc/ssl/certs/ca-certificates.crt
# Mozilla's certificate data is under the MPL 2.0, which requires that
# a copy of the data tell its recipient so and where the license is.
COPY --chmod=u=rwX,go=rX copyright /usr/share/doc/mozilla-ca/copyright
