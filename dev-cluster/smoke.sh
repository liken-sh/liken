#!/usr/bin/env bash
#
# The smoke drill: prove that a machine and blank disks become a Ready
# cluster node, with no human watching.
#
# This is the boot CI runs (`make smoke` from the repo root), and it
# is deliberately the same boot a developer runs by hand: the run
# target below is the ordinary one, with two differences an automated
# caller needs. The serial console goes to a file instead of a
# terminal (CONSOLE=file:...), and the verdict comes from outside the
# machine: we poll the cluster's API through the leader's forwarded
# port and pass when the node reports Ready. That is the same
# interface a human operator uses, and "the node is Ready" is exactly
# the claim liken makes — not "QEMU exited", which a crashed machine
# also achieves.
#
# kubectl comes from the vendored k3s binary: the same file the image
# packs as the machine's whole Kubernetes also runs fine on the build
# host, and `k3s kubectl` is a complete kubectl. The credential is the
# admin kubeconfig minted offline from the deployment's identity
# (../identity/kubeconfig.sh); the machine is never asked for one.
#
# The drill starts from nothing: node-1's disks are deleted first,
# because the claim under test includes claiming and formatting blank
# disks. Locally that is a factory reset of node-1 — its cluster
# state, pod storage, and installed system are gone. That is what a
# drill machine is for, but it is worth knowing before you run this
# next to state you care about.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$here"

K3S_VERSION="$(<../k3s/VERSION)"
K3S="../k3s/dist/${K3S_VERSION}/k3s"
KUBECONFIG_FILE="identity/kubeconfig"
CONSOLE_LOG="guests/node-1/console.log"

# How long the node gets to become Ready. Six minutes is generous for
# a KVM boot (locally this takes about two); the margin covers a CI
# runner's slower disks and shared CPUs. Override for experiments:
# SMOKE_DEADLINE=30 makes a handy rehearsal of the failure path.
SMOKE_DEADLINE="${SMOKE_DEADLINE:-360}"

for f in "$K3S" "$KUBECONFIG_FILE"; do
    [[ -e "$f" ]] || {
        echo "smoke.sh: missing $f — run \`make smoke\` from the repo root," >&2
        echo "which builds the artifacts and mints the kubeconfig first" >&2
        exit 1
    }
done

# The factory reset described above.
rm -rf guests/node-1
mkdir -p guests/node-1

# Boot the machine in the background, in its own session (setsid), so
# that one signal can later end the whole process tree: make, the
# shell it spawns, and QEMU. The guest's serial console goes to the
# console log; make's and QEMU's own chatter goes to a separate file,
# because QEMU owns the console file and a second writer would
# corrupt it.
setsid make run NODE=node-1 BOOT=kernel \
    CONSOLE="file:$CONSOLE_LOG" \
    </dev/null >guests/node-1/qemu.log 2>&1 &
guest=$!

# Whatever happens below — success, timeout, an unexpected error —
# the guest must not outlive the drill. Killing the process group
# (the negative PID) reaches QEMU even though we started make.
teardown() {
    kill -TERM -- "-$guest" 2>/dev/null || true
    wait "$guest" 2>/dev/null || true
}
trap teardown EXIT

# Poll for the verdict. Each attempt is bounded (--request-timeout)
# so a half-open connection can't eat the deadline, and failures are
# expected for the first minute or so: the API server isn't listening
# until k3s is well into its own startup.
started="$(date +%s)"
echo "smoke: booting node-1, waiting up to ${SMOKE_DEADLINE}s for Ready"
while true; do
    if "$K3S" kubectl --kubeconfig "$KUBECONFIG_FILE" \
            --request-timeout=5s get nodes --no-headers 2>/dev/null \
            | awk '$2 == "Ready" { found = 1 } END { exit !found }'; then
        elapsed=$(( $(date +%s) - started ))
        echo "smoke: node-1 is Ready after ${elapsed}s"
        exit 0
    fi

    if (( $(date +%s) - started >= SMOKE_DEADLINE )); then
        echo "smoke: node-1 was not Ready within ${SMOKE_DEADLINE}s" >&2
        echo "smoke: the last of the console ($CONSOLE_LOG):" >&2
        tail -n 40 "$CONSOLE_LOG" >&2 || true
        exit 1
    fi

    sleep 5
done
