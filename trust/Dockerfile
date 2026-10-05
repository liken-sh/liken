# The trust image: the CA bundle at the path where OpenSSL, GnuTLS,
# libcurl, and Go read it, and its notice. An image that verifies TLS
# copies both:
#
#   COPY --from=trust / /
#
# The snapshot is the one that VERSION names, fetched by the checksum
# that curl.se publishes beside it.
FROM scratch AS fetch
ADD --checksum=sha256:a41b5d356aea97a529fe27e0f7316d2f9d946d75927476cf9cf1b90637d00505 \
    https://curl.se/ca/cacert-2026-09-25.pem /cacert.pem

# --chmod applies to the directories that COPY creates as well, so the
# mode is symbolic: X sets execute on the directories alone.
FROM scratch
COPY --from=fetch --chmod=u=rwX,go=rX /cacert.pem /etc/ssl/certs/ca-certificates.crt
# Mozilla's certificate data is under the MPL 2.0, which requires that
# a copy of the data tell its recipient so and where the license is.
COPY --chmod=u=rwX,go=rX copyright /usr/share/doc/mozilla-ca/copyright
