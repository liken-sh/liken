#!/usr/bin/env bash
#
# The BIOS smoke drill. It proves that a machine can install itself
# onto a blank disk, then boot that disk under BIOS firmware, all the
# way to a Ready cluster node, with no person watching.
#
# This drill covers what the UEFI smoke drill (smoke-uefi.sh) does
# not cover on purpose. That drill boots through -kernel, with QEMU
# acting as the bootloader, so it proves the operating system but
# never touches the install path or the disk boot path. This drill
# starts further back: the machine installs itself first (it claims
# the blank boot disk, lays down GRUB's boot sectors and config, and
# copies the release into slot A). After that, only the boot chain
# that the installer wrote can bring the node up: SeaBIOS runs the
# MBR's 440 bytes, those bytes load GRUB's core image from the raw
# biosBoot partition, GRUB reads its config from the bootHome
# filesystem, and the chosen slot's kernel boots. A node that reports
# Ready proves every link in that chain.
#
# SeaBIOS is QEMU's default firmware, so "under BIOS" means only the
# lab's FIRMWARE=bios: no OVMF pflash drives, and nothing else
# different. The verdict comes the same way as in the UEFI drill: the
# script polls the cluster's API through the leader's forwarded port
# until the node reports Ready.
#
# Like smoke-uefi.sh, this drill performs a factory reset of node-1:
# it deletes the node's disks first, because claiming blank disks is
# part of the claim under test.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$here"

K3S_VERSION="$(<../k3s/VERSION)"
K3S="../k3s/dist/${K3S_VERSION}/k3s"
KUBECONFIG_FILE="identity/kubeconfig"
CONSOLE_LOG="guests/node-1/console.log"
INSTALL_LOG="guests/node-1/install-console.log"

# The Ready deadline, as in smoke-uefi.sh. The install boot gets its
# own larger bound below; this deadline covers only the disk boot.
SMOKE_DEADLINE="${SMOKE_DEADLINE:-120}"

for f in "$K3S" "$KUBECONFIG_FILE" image/install.cpio; do
    [[ -e "$f" ]] || {
        echo "smoke-bios.sh: missing $f — run \`make smoke-bios\` from the repo root," >&2
        echo "which builds the artifacts and mints the kubeconfig first" >&2
        exit 1
    }
done

# The factory reset described above.
rm -rf guests/node-1
mkdir -p guests/node-1

# On failure, return the evidence: whichever console logs exist, and
# QEMU's own output. QEMU's output holds the explanation for cases
# where the machine never got far enough to write a console log.
evidence() {
    for log in "$INSTALL_LOG" "$CONSOLE_LOG"; do
        if [[ -e "$log" ]]; then
            echo "smoke-bios: the last of $log:" >&2
            tail -n 40 "$log" >&2
        fi
    done
    echo "smoke-bios: the last of QEMU's own output (guests/node-1/qemu.log):" >&2
    tail -n 20 guests/node-1/qemu.log >&2 || true
}

# Step one: the install. This is a bounded boot: the installer powers
# the machine off when it finishes, and -no-reboot makes QEMU exit
# with it. Because of this, the script runs it in the foreground, and
# treats its exit as the signal to move on. `timeout` bounds the case
# where the install hangs instead of finishing. TERM reaches the
# whole process group, and --kill-after covers a QEMU process that
# ignores TERM.
echo "smoke-bios: installing node-1 (BIOS)"
if ! timeout --foreground --kill-after=10 180 \
        make install NODE=node-1 FIRMWARE=bios \
        CONSOLE="file:$INSTALL_LOG" \
        </dev/null >guests/node-1/qemu.log 2>&1; then
    echo "smoke-bios: the install boot failed or timed out" >&2
    evidence
    exit 1
fi

# The installer's last log line is the proof that the install
# completed, rather than the machine powering off some other way.
if ! grep -q "liken: install complete" "$INSTALL_LOG"; then
    echo "smoke-bios: the install boot exited without completing" >&2
    evidence
    exit 1
fi
echo "smoke-bios: install complete; booting the installed disk"

# Step two: boot the disk that the installer just wrote, in the same
# way smoke-uefi.sh boots its guest: in the background, in its own
# session, so one signal can end the whole process tree.
setsid make run NODE=node-1 BOOT=disk FIRMWARE=bios \
    CONSOLE="file:$CONSOLE_LOG" \
    </dev/null >guests/node-1/qemu.log 2>&1 &
guest=$!

teardown() {
    kill -TERM -- "-$guest" 2>/dev/null || true
    wait "$guest" 2>/dev/null || true
}
trap teardown EXIT

# Poll for the verdict, the same way smoke-uefi.sh does: bounded
# attempts, an early exit if the guest dies, and the deadline for
# everything else.
started="$(date +%s)"
echo "smoke-bios: waiting up to ${SMOKE_DEADLINE}s for Ready"
while true; do
    if "$K3S" kubectl --kubeconfig "$KUBECONFIG_FILE" \
            --request-timeout=5s get nodes --no-headers 2>/dev/null \
            | awk '$2 == "Ready" { found = 1 } END { exit !found }'; then
        elapsed=$(( $(date +%s) - started ))
        echo "smoke-bios: node-1 is Ready after ${elapsed}s, booted from disk under BIOS"
        exit 0
    fi

    if ! kill -0 "$guest" 2>/dev/null; then
        echo "smoke-bios: the guest exited before its node became Ready" >&2
        evidence
        exit 1
    fi

    if (( $(date +%s) - started >= SMOKE_DEADLINE )); then
        echo "smoke-bios: node-1 was not Ready within ${SMOKE_DEADLINE}s" >&2
        evidence
        exit 1
    fi

    sleep 5
done
