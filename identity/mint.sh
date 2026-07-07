#!/usr/bin/env bash
#
# Mint a machine's identity: the certificate authorities k3s would
# otherwise generate for itself on first boot.
#
# Kubernetes trust is built from several small PKIs, each covering one
# relationship. k3s checks for these files before generating its own,
# so placing them in /var/lib/rancher/k3s/server/tls ahead of first
# start reverses the usual flow. Normally the identity is an output
# that has to be extracted from a running machine, which a machine
# with no shell could never hand over anyway; here it is an input the
# image carries. Everything k3s signs from here on (the API server's
# serving cert, kubelet certs, all of it) chains up to keys we held
# before the machine ever booted, which is what lets an operator's
# kubeconfig be computed offline (see kubeconfig.sh).
#
# The authorities:
#
#   server-ca          signs the API server's serving certificates,
#                      the thing kubectl verifies before trusting a
#                      connection
#   client-ca          signs client certificates. The API server reads
#                      identity out of the subject: CN is the username,
#                      each O is a group membership
#   request-header-ca  the aggregation layer's trust root: extension
#                      API servers accept proxied-authentication
#                      headers only from a front proxy bearing a cert
#                      from this CA
#   etcd/server-ca     etcd's two PKIs. liken's k3s keeps state in
#   etcd/peer-ca       sqlite via kine, not etcd, but k3s manages the
#                      full CA family as a set
#   service.key        not a CA: the key that signs every
#                      ServiceAccount token. Whoever holds this key
#                      can mint valid identities for any pod
#
# Everything is ECDSA P-256, matching what k3s generates for itself.
# Ten-year lifetimes: these are roots for a learning distro, and
# rotation is a problem for a later milestone.
#
# Each artifact is minted only if it doesn't already exist, so adding
# a new artifact to this script (or re-running it for any reason)
# never replaces an identity that machines already carry: replacing
# the CAs would orphan every kubeconfig computed from them, and
# replacing the token would strand any machine that hasn't joined
# yet. Replacing the identity is a deliberate act: run `make clean`
# here, and the next build mints a new one.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tls="$here/dist/tls"
mkdir -p "$tls/etcd"

# One self-signed root per authority. -x509 makes `req` emit a
# certificate directly instead of a signing request; the extensions
# mark it as a CA whose key may sign other certificates.
new_ca() {
    local path="$1" cn="$2"
    if [[ -f "$tls/$path.crt" ]]; then
        echo "keeping $path: $cn"
        return
    fi
    openssl req -x509 -new -nodes \
        -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
        -keyout "$tls/$path.key" \
        -out "$tls/$path.crt" \
        -days 3650 \
        -subj "/CN=$cn" \
        -addext "basicConstraints=critical,CA:TRUE" \
        -addext "keyUsage=critical,digitalSignature,keyCertSign,cRLSign" \
        2>/dev/null
    echo "minted $path: $cn"
}

new_ca server-ca "liken server CA"
new_ca client-ca "liken client CA"
new_ca request-header-ca "liken request-header CA"
new_ca etcd/server-ca "liken etcd server CA"
new_ca etcd/peer-ca "liken etcd peer CA"

# The ServiceAccount signing key is not like the CAs above: tokens are
# JWTs, so there's no certificate, just a keypair the API server signs
# with and verifies against. The encoding matters: kube-apiserver reads this
# file with a parser that understands the older SEC1 encoding
# ("EC PRIVATE KEY", which ecparam emits) but not PKCS#8 ("PRIVATE
# KEY", which genpkey emits); given PKCS#8 it fails on startup with an
# error that the file contains no valid keys.
if [[ -f "$tls/service.key" ]]; then
    echo "keeping service.key: the ServiceAccount token signing key"
else
    openssl ecparam -name prime256v1 -genkey -noout \
        -out "$tls/service.key" 2>/dev/null
    echo "minted service.key: the ServiceAccount token signing key"
fi

# The cluster's join token, in k3s's "secure" format:
#
#   K10<CA-HASH>::<user>:<password>
#
# Normally this has to be copied off a running server
# (/var/lib/rancher/k3s/server/node-token), because the CA it hashes
# doesn't exist until k3s generates it at first boot. liken reverses
# that: the server CA is minted above, before any machine exists, so
# the whole token is computable right here. The CA-HASH is the SHA256
# of the cluster CA certificate. A joining machine fetches the
# server's CA bundle, hashes it, and compares before it trusts the
# endpoint or presents the secret, so the token authenticates in both
# directions: the machine proves itself to the cluster, and the
# cluster proves itself to the machine. The secret half is 32 hex
# characters of real randomness, the same format k3s generates.
# "server" is the credential's username: whoever bears this token may
# join machines to the cluster.
if [[ -f "$here/dist/token" ]]; then
    echo "keeping token: the cluster join token"
else
    ca_hash="$(sha256sum "$tls/server-ca.crt" | cut -d' ' -f1)"
    secret="$(openssl rand -hex 16)"
    printf 'K10%s::server:%s\n' "$ca_hash" "$secret" >"$here/dist/token"
    chmod 600 "$here/dist/token"
    echo "minted token: the cluster join token"
fi
