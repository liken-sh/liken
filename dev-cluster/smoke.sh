#!/usr/bin/env bash
#
# The smoke drill. It proves that a machine and blank disks become a
# Ready cluster node, with no person watching. The machine installs
# itself onto a blank disk first, then boots that disk under real
# firmware. The argument picks the firmware dialect: `smoke.sh uefi`
# runs the drill under OVMF, and `smoke.sh bios` runs it under
# SeaBIOS. CI runs both (`make smoke-uefi` and `make smoke-bios` from
# the repo root).
#
# The drill has two steps. In step one, the machine installs itself:
# it boots the install image through -kernel, with QEMU as the
# bootloader, claims the blank boot disk, copies the release into
# slot A, writes the boot chain for its firmware, and powers off. In
# step two, the machine boots from that disk, with no -kernel help.
# Only the boot chain that the installer wrote can bring the node up.
# This is the arrangement that real machines use: the OS runs from a
# read-only system image on the machine's own disk, and the 1 GB
# memory envelope holds because no copy of the OS sits in RAM.
#
# The two dialects prove two different boot chains. Under UEFI, OVMF
# reads the boot entries that the installer wrote into NVRAM. Each
# entry loads the slot's kernel through its EFI stub, and the stub
# loads the initrd= files that the entry's command line names. Under
# BIOS, SeaBIOS runs the MBR's 440 bytes, those bytes load GRUB's
# core image from the raw biosBoot partition, GRUB reads its config
# from the bootHome filesystem, and the chosen slot's kernel boots. A
# node that reports Ready proves every link in its chain.
#
# The verdict comes from outside the machine: the script polls the
# cluster's API through the leader's forwarded port, and passes when
# the node reports Ready. This is the same interface that a human
# operator uses, and "the node is Ready" is exactly the claim that
# liken makes. It is not the same claim as "QEMU exited", which a
# crashed machine can also produce.
#
# kubectl comes from the vendored k3s binary. The same file that the
# image packs as the machine's whole Kubernetes also runs fine on the
# build host, and `k3s kubectl` is a complete kubectl. The credential
# is the admin kubeconfig, minted offline from the deployment's
# identity (../identity/kubeconfig.go); the script never asks the
# machine for one.
#
# The drill starts from nothing: it deletes node-1's disks first,
# because the claim under test includes claiming and formatting blank
# disks. Locally, this is a factory reset of node-1: its cluster
# state, pod storage, and installed system are gone afterward. That
# is what a drill machine is for, but it is worth knowing before you
# run this drill next to state that you care about.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$here"

FIRMWARE="${1:-}"
case "$FIRMWARE" in
    uefi | bios) ;;
    *)
        echo "usage: smoke.sh <uefi|bios>" >&2
        exit 2
        ;;
esac
drill="smoke-$FIRMWARE"

K3S_VERSION="$(<../k3s/VERSION)"
K3S="../k3s/dist/${K3S_VERSION}/k3s"
KUBECONFIG_FILE="identity/kubeconfig"
CONSOLE_LOG="guests/node-1/console.log"
INSTALL_LOG="guests/node-1/install-console.log"

# How long the node gets to become Ready. A KVM boot reaches Ready in
# under half a minute. Two minutes covers a CI runner's slower disks
# and shared CPUs. The install boot gets its own larger bound below;
# this deadline covers only the disk boot. The script catches a guest
# that dies outright immediately, so this deadline decides only how
# long a hung boot can hold up the run. Override this value for
# experiments: SMOKE_DEADLINE=10 gives a quick way to rehearse the
# failure path.
SMOKE_DEADLINE="${SMOKE_DEADLINE:-120}"

for f in "$K3S" "$KUBECONFIG_FILE" image/install.cpio; do
    [[ -e "$f" ]] || {
        echo "$drill: missing $f — run \`make $drill\` from the repo root," >&2
        echo "which builds the artifacts and mints the kubeconfig first" >&2
        exit 1
    }
done

# The factory reset described above.
rm -rf guests/node-1
mkdir -p guests/node-1

# On failure, return the evidence: whichever console logs exist, and
# QEMU's own output. QEMU's output holds the explanation for cases
# where the machine never got far enough to write a console log, for
# example because of a missing host tool, a bad flag, or missing
# firmware.
evidence() {
    for log in "$INSTALL_LOG" "$CONSOLE_LOG"; do
        if [[ -e "$log" ]]; then
            echo "$drill: the last of $log:" >&2
            tail -n 40 "$log" >&2
        fi
    done
    echo "$drill: the last of QEMU's own output (guests/node-1/qemu.log):" >&2
    tail -n 20 guests/node-1/qemu.log >&2 || true
}

# Step one: the install. This is a bounded boot: the installer powers
# the machine off when it finishes, and -no-reboot makes QEMU exit
# with it. Because of this, the script runs it in the foreground, and
# treats its exit as the signal to move on. `timeout` bounds the case
# where the install hangs instead of finishing. TERM reaches the
# whole process group, and --kill-after covers a QEMU process that
# ignores TERM.
echo "$drill: installing node-1 ($FIRMWARE)"
if ! timeout --foreground --kill-after=10 180 \
        make install NODE=node-1 FIRMWARE="$FIRMWARE" \
        CONSOLE="file:$INSTALL_LOG" \
        </dev/null >guests/node-1/qemu.log 2>&1; then
    echo "$drill: the install boot failed or timed out" >&2
    evidence
    exit 1
fi

# The installer's last log line is the proof that the install
# completed, rather than the machine powering off some other way.
if ! grep -q "liken: install complete" "$INSTALL_LOG"; then
    echo "$drill: the install boot exited without completing" >&2
    evidence
    exit 1
fi
echo "$drill: install complete; booting the installed disk"

# Step two: boot the disk that the installer just wrote. The boot
# runs in the background, in its own session (setsid), so that one
# signal can later end the whole process tree: make, the shell that
# make spawns, and QEMU. The guest's serial console goes to the
# console log. make's own chatter and QEMU's own chatter go to a
# separate file, because QEMU owns the console file, and a second
# writer would corrupt it.
setsid make run NODE=node-1 BOOT=disk FIRMWARE="$FIRMWARE" \
    CONSOLE="file:$CONSOLE_LOG" \
    </dev/null >guests/node-1/qemu.log 2>&1 &
guest=$!

# Whatever happens below (success, timeout, or an unexpected error),
# the guest must not outlive the drill. Killing the process group
# (the negative PID) reaches QEMU, even though the script started
# make instead.
teardown() {
    kill -TERM -- "-$guest" 2>/dev/null || true
    wait "$guest" 2>/dev/null || true
}
trap teardown EXIT

# Poll for the verdict. Each attempt is bounded (--request-timeout),
# so a half-open connection cannot consume the whole deadline, and
# failures are expected at first: the API server does not listen
# until k3s is well into its own startup. A guest that has died can
# never become Ready, so the script fails the drill immediately in
# that case, instead of waiting out the deadline.
started="$(date +%s)"
echo "$drill: waiting up to ${SMOKE_DEADLINE}s for Ready"
while true; do
    if "$K3S" kubectl --kubeconfig "$KUBECONFIG_FILE" \
            --request-timeout=5s get nodes --no-headers 2>/dev/null \
            | awk '$2 == "Ready" { found = 1 } END { exit !found }'; then
        elapsed=$(( $(date +%s) - started ))
        echo "$drill: node-1 is Ready after ${elapsed}s, booted from disk under $FIRMWARE"
        exit 0
    fi

    if ! kill -0 "$guest" 2>/dev/null; then
        echo "$drill: the guest exited before its node became Ready" >&2
        evidence
        exit 1
    fi

    if (( $(date +%s) - started >= SMOKE_DEADLINE )); then
        echo "$drill: node-1 was not Ready within ${SMOKE_DEADLINE}s" >&2
        evidence
        exit 1
    fi

    sleep 5
done
