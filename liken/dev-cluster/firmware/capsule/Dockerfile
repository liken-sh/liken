# The image that builds the capsule firmware and its test catalog.
# build.sh and catalog.sh run every step in it, so the host needs only
# docker and git.
#
# The firmware binary depends on the compiler, nasm, and iasl versions,
# so the base is pinned by digest and every package comes from one
# dated snapshot of the Ubuntu archive. The same two pins give the same
# toolchain on every laptop, and a new toolchain is a new date here.
#
# The build context is trust/dist/<version>/ at the top of the
# repository: the CA bundle that the trust component pins. The Ubuntu
# image has no CA bundle, and snapshot.ubuntu.com answers only over
# HTTPS.
FROM ubuntu:24.04@sha256:68ee22004a9109b44973313b02ff7b1de250f1de0f5ecb628d73f579aaff33ac
ENV SNAPSHOT=20261015
COPY cacert.pem /etc/ssl/certs/ca-certificates.crt
# The edk2 build needs gcc, nasm, iasl (acpica-tools), libuuid, and
# Python. The catalog needs openssl for edk2's capsule signing tool,
# gcab for the cabinets, jcat-tool to sign the catalog, and zstd to
# compress it.
RUN echo 'Acquire::https::CAInfo "/etc/ssl/certs/ca-certificates.crt";' >/etc/apt/apt.conf.d/99snapshot-ca \
    && rm -f /etc/apt/sources.list /etc/apt/sources.list.d/* \
    && printf '%s\n' \
        'Types: deb' \
        "URIs: https://snapshot.ubuntu.com/ubuntu/${SNAPSHOT}T000000Z/" \
        'Suites: noble noble-updates noble-security' \
        'Components: main universe' \
        'Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg' \
        >/etc/apt/sources.list.d/snapshot.sources \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        build-essential uuid-dev nasm acpica-tools python3 python3-setuptools python-is-python3 \
        openssl gcab jcat zstd \
    && rm -rf /var/lib/apt/lists/*
