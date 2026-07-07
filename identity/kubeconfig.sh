#!/usr/bin/env bash
#
# Compute an operator's kubeconfig offline, from the identity this
# repo minted. The machine is never asked for a credential. Pre-seeding
# the CAs (mint.sh) exists precisely so that the credential can be
# computed without the machine's help.
#
# A kubeconfig is three facts:
#
#   1. where the cluster is (a URL),
#   2. why to believe it's really the cluster (the server CA that
#      signed its serving cert),
#   3. who we are (a client certificate the cluster's client CA
#      signed).
#
# The identity in a client certificate lives in its subject: the API
# server takes CN as the username and every O as a group. There is no
# user database behind this. Presenting a cert with O=system:masters
# makes the bearer a cluster admin, because RBAC binds that group to
# cluster-admin; the certificates themselves are the only user records.
#
# The result is written to dist/kubeconfig and nowhere else: liken
# never touches ~/.kube/config or any other kubeconfig the operator
# already has. Point kubectl at it explicitly:
#
#   kubectl --kubeconfig identity/dist/kubeconfig get nodes

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tls="$here/dist/tls"

# Where the cluster is: QEMU forwards this host port to the guest's
# API server (the Makefile's `run` target defines that mapping). The
# serving cert k3s mints covers 127.0.0.1 by default, so the forwarded
# connection verifies without any extra SANs.
server="https://127.0.0.1:16443"

# Who we are: a fresh keypair, and a certificate for it signed by the
# client CA. The CSR carries the subject, but the subject is only a
# claim; the CA's signature is what makes the API server accept it.
openssl req -new -nodes \
    -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
    -keyout "$tls/admin.key" \
    -out "$tls/admin.csr" \
    -subj "/CN=admin/O=system:masters" \
    2>/dev/null

# A client cert needs the clientAuth extended key usage; the API
# server rejects certificates that don't declare what they're for.
# The certificate lasts one year, which is generous for a development
# credential; re-running `make kubeconfig` mints a new one in seconds.
openssl x509 -req \
    -in "$tls/admin.csr" \
    -CA "$tls/client-ca.crt" -CAkey "$tls/client-ca.key" \
    -days 365 \
    -extfile <(echo "extendedKeyUsage=clientAuth") \
    -out "$tls/admin.crt" \
    2>/dev/null
rm "$tls/admin.csr"

# The kubeconfig itself, with the certificates embedded (base64, like
# every kubeconfig) so the file is self-contained and portable.
b64() { base64 -w0 <"$1"; }

cat >"$here/dist/kubeconfig" <<EOF
apiVersion: v1
kind: Config
clusters:
  - name: liken
    cluster:
      server: $server
      certificate-authority-data: $(b64 "$tls/server-ca.crt")
contexts:
  - name: liken
    context:
      cluster: liken
      user: admin
current-context: liken
users:
  - name: admin
    user:
      client-certificate-data: $(b64 "$tls/admin.crt")
      client-key-data: $(b64 "$tls/admin.key")
EOF

echo "wrote dist/kubeconfig for admin (O=system:masters) at $server"
