#!/usr/bin/env bash
#
# Mint a machine's identity: the certificate authorities k3s would
# otherwise invent for itself on first boot.
#
# Kubernetes trust is a handful of tiny PKIs, each guarding one
# relationship. k3s checks for these files before generating its own,
# so placing them in /var/lib/rancher/k3s/server/tls ahead of first
# start inverts the usual flow: identity becomes an input the image
# carries, not an output that has to be extracted from a running
# machine (which a machine with no shell could never hand over
# anyway). Everything k3s signs from here on — the API server's
# serving cert, kubelet certs, the works — chains up to keys we held
# before the machine ever booted, which is what lets an operator's
# kubeconfig be computed offline (see kubeconfig.sh).
#
# The cast of authorities:
#
#   server-ca          signs the API server's serving certificates —
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
#                      ServiceAccount token. Possession of this key is
#                      the power to mint valid identities for any pod
#
# Everything is ECDSA P-256, matching what k3s generates for itself.
# Ten-year lifetimes: these are roots for a learning distro, and
# rotation is a lesson for another milestone.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tls="$here/dist/tls"
mkdir -p "$tls/etcd"

# One self-signed root per authority. -x509 makes `req` emit a
# certificate directly instead of a signing request; the extensions
# mark it as a CA whose key may sign other certificates.
new_ca() {
    local path="$1" cn="$2"
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

# The ServiceAccount signing key stands alone — tokens are JWTs, so
# there's no certificate, just a keypair the API server signs with and
# verifies against. The format is load-bearing: kube-apiserver reads
# this file with a parser that understands the older SEC1 encoding
# ("EC PRIVATE KEY", which ecparam emits) but not PKCS#8 ("PRIVATE
# KEY", which genpkey emits) — with PKCS#8 it dies on startup claiming
# the file contains no valid keys.
openssl ecparam -name prime256v1 -genkey -noout \
    -out "$tls/service.key" 2>/dev/null
echo "minted service.key: the ServiceAccount token signing key"
