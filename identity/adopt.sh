#!/usr/bin/env bash
#
# Adopt an existing cluster's identity: the inverse of mint.sh.
#
# The image carries the cluster's certificate authorities and join
# token, which is why machines built from the same image belong to
# the same cluster. mint.sh creates that identity before any machine
# exists, which works for a cluster liken founds. For a cluster liken
# did not create, the identity already exists on that cluster's
# servers and has to be copied off one of them instead. This script
# takes that copy and lays it into the deployment's identity
# directory exactly as mint.sh would have laid a minted one, so
# everything downstream (kubeconfig.sh, the image build, init's
# seeding of /var/lib/rancher/k3s/server/tls) is identical whether
# the identity was minted or adopted. An image built from an adopted
# identity joins the existing cluster: its machines present the real
# token, and every certificate they see chains to the real CAs.
#
# Harvesting, run as root on any server of the existing cluster:
#
#   cd /var/lib/rancher/k3s/server
#   tar czf /tmp/identity.tgz token \
#       tls/server-ca.{crt,key} \
#       tls/client-ca.{crt,key} \
#       tls/request-header-ca.{crt,key} \
#       tls/service.key \
#       tls/etcd/server-ca.{crt,key} \
#       tls/etcd/peer-ca.{crt,key}
#
# then unpack that archive somewhere private and point this script at
# the directory. Only the certificate *authorities* and the token
# come over: the tls directory on a live server also holds the leaf
# certificates k3s signed from them (the API server's serving cert,
# kubelet certs), and those stay behind, because every server signs
# its own leaves from the shared roots. The service.key rides along
# for the same reason it exists in mint.sh: it signs every
# ServiceAccount token, and a control plane that verified tokens
# against a different key would reject every pod's identity.
#
# The token file on a running server is in k3s's "secure" format,
# K10<CA-HASH>::<user>:<password>, where CA-HASH is the SHA256 of the
# cluster CA certificate. That hash is checkable right here, and this
# script checks it: a token harvested from one cluster and CAs from
# another is exactly the mixup that would otherwise surface as every
# machine refusing to join.

set -euo pipefail

if [[ $# -ne 2 || ! -d "${1:-}" ]]; then
    echo "usage: $0 <harvest-dir> <identity-dir>" >&2
    echo "  where <harvest-dir> holds token and tls/... copied from an" >&2
    echo "  existing server's /var/lib/rancher/k3s/server (see comments)" >&2
    echo "  and <identity-dir> is the deployment's identity directory" >&2
    exit 64
fi

harvest="$(cd "$1" && pwd)"
out="$2"

# The bundle, expressed as paths relative to both the harvest and
# dist/: the same set mint.sh produces, no more.
bundle=(
    token
    tls/server-ca.crt tls/server-ca.key
    tls/client-ca.crt tls/client-ca.key
    tls/request-header-ca.crt tls/request-header-ca.key
    tls/service.key
    tls/etcd/server-ca.crt tls/etcd/server-ca.key
    tls/etcd/peer-ca.crt tls/etcd/peer-ca.key
)

# Refuse a partial harvest before touching anything: a bundle missing
# one CA would produce an image that boots and then fails in some
# distant, confusing way (a control plane that can't sign one kind of
# certificate, pods whose tokens don't verify).
for f in "${bundle[@]}"; do
    if [[ ! -f "$harvest/$f" ]]; then
        echo "harvest is missing $f; re-run the tar on the existing server" >&2
        exit 65
    fi
done

# The cross-check: the token's embedded CA hash must match the
# harvested server CA. This is the same verification a joining
# machine performs before trusting an endpoint, done early, where the
# fix (re-harvest, from one server this time) is cheap.
token="$(<"$harvest/token")"
if [[ "$token" == K10* ]]; then
    ca_hash="$(sha256sum "$harvest/tls/server-ca.crt" | cut -d' ' -f1)"
    if [[ "$token" != "K10$ca_hash"::* ]]; then
        echo "the harvested token does not hash the harvested server CA;" >&2
        echo "these came from different clusters" >&2
        exit 65
    fi
fi

# Replacing an identity is a deliberate act, same as mint.sh: an
# image built from a mix of two identities could not join either
# cluster. If the identity directory holds anything, stop and make
# the operator choose.
for f in "${bundle[@]}"; do
    if [[ -e "$out/$f" ]]; then
        echo "$out/$f already exists; this deployment already holds an identity." >&2
        echo "delete the identity directory first if replacing it is really the intent" >&2
        exit 65
    fi
done

for f in "${bundle[@]}"; do
    mkdir -p "$out/$(dirname "$f")"
    cp "$harvest/$f" "$out/$f"
    echo "adopted $f"
done

# Private keys and the token are secrets; the certificates are not,
# but locking down the whole bundle is simpler than itemizing which
# files need it.
chmod -R go-rwx "$out"

echo "the identity is adopted: images built from it join the existing cluster"
