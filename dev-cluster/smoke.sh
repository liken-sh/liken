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
# slot A, writes the boot chain for its firmware, and holds the console
# for the person who is watching an install. The drill watches the
# console log for the installer's success line and tears the guest down
# once the install is done. In step two, the machine boots from that
# disk, with no -kernel help.
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
        echo "usage: smoke.sh <uefi|bios> [drill-label]" >&2
        exit 2
        ;;
esac

# The drill's name. It defaults to the firmware dialect (smoke-uefi,
# smoke-bios), which is what the two firmware drills prove. A caller
# that proves something else passes its own label: the hardware-parity
# drill passes "hardware", so its output and its evidence read
# smoke-hardware.
drill="smoke-${2:-$FIRMWARE}"

# The hardware shape and the install image the drill boots. Both
# default to the paravirtual lab: virtio controllers and the dev
# cluster's own install image. The hardware-parity drill overrides them
# from dev-cluster/Makefile (HARDWARE=metal and the hardware
# deployment's install image) so the same script drives the metal
# shape. HARDWARE rides on every make invocation below; INSTALL_CPIO
# matters only to the install boot, which is the boot that carries an
# initramfs.
#
# REPORT_STICK, when set, is a stick image to drill the hardware
# report against before the install: the drill boots liken.report with
# a copy of this stick attached over USB, and checks the proposal the
# report writes onto it. Empty skips the report step, which is the
# right default for the firmware drills: the report's value is the
# module path, so it proves the most on the metal shape.
HARDWARE="${HARDWARE:-virtio}"
INSTALL_CPIO="${INSTALL_CPIO:-image/install.cpio}"
REPORT_STICK="${REPORT_STICK:-}"

K3S_VERSION="$(<../k3s/VERSION)"
K3S="../k3s/dist/${K3S_VERSION}/k3s"
KUBECONFIG_FILE="identity/kubeconfig"
CONSOLE_LOG="guests/node-1/console.log"
INSTALL_LOG="guests/node-1/install-console.log"
REPORT_LOG="guests/node-1/report-console.log"

# How long the node gets to become Ready. A KVM boot reaches Ready in
# under half a minute. Two minutes covers a CI runner's slower disks
# and shared CPUs. The install boot gets its own larger bound below;
# this deadline covers only the disk boot. The script catches a guest
# that dies outright immediately, so this deadline decides only how
# long a hung boot can hold up the run. Override this value for
# experiments: SMOKE_DEADLINE=10 gives a quick way to rehearse the
# failure path.
SMOKE_DEADLINE="${SMOKE_DEADLINE:-120}"

for f in "$K3S" "$KUBECONFIG_FILE" "$INSTALL_CPIO"; do
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
    for log in "$REPORT_LOG" "$INSTALL_LOG" "$CONSOLE_LOG"; do
        if [[ -e "$log" ]]; then
            echo "$drill: the last of $log:" >&2
            tail -n 40 "$log" >&2
        fi
    done
    echo "$drill: the last of QEMU's own output (guests/node-1/qemu.log):" >&2
    tail -n 20 guests/node-1/qemu.log >&2 || true
}

# The install and report boots are attended boots: they finish their
# work, sync it, and then hold the console for a person to acknowledge
# before they stop. A person proved they were present when they picked
# the menu entry, so the message waits for them instead of vanishing
# behind a power-off on a timer. A drill has no person, so these two
# helpers stand in for one: the drill runs an attended boot in the
# background and watches its console log for the boot's own verdict
# line. The verdict prints only after the boot's work has reached the
# disk and been synced, so ending the guest at that moment loses
# nothing.
#
# teardown_group ends one attended guest. setsid (below) gave the boot
# its own session, so one SIGTERM to the process group ends the whole
# tree: make, the shell make spawned, and QEMU.
teardown_group() {
    local leader="$1"
    kill -TERM -- "-$leader" 2>/dev/null || true
    wait "$leader" 2>/dev/null || true
    # wait wakes when make exits, but QEMU is a sibling in the same
    # session and takes a moment longer to flush and release its disk
    # locks. The next boot opens the same disks, so it must not start
    # while this QEMU still holds them. Poll the process group until
    # it is empty. The bound keeps a stuck guest from hanging the
    # drill.
    for _ in $(seq 1 50); do
        kill -0 -- "-$leader" 2>/dev/null || break
        sleep 0.2
    done
}

# watch_hold waits for an attended boot's verdict: the success line
# tears the guest down and returns; the failure line, a guest that
# dies first, or the deadline ends the drill with the evidence.
watch_hold() {
    local leader="$1" log="$2" success="$3" failure="$4" what="$5"
    local started
    started="$(date +%s)"
    while true; do
        if grep -q "$success" "$log" 2>/dev/null; then
            teardown_group "$leader"
            return 0
        fi
        if grep -q "$failure" "$log" 2>/dev/null; then
            teardown_group "$leader"
            echo "$drill: $what reported a failure" >&2
            evidence
            exit 1
        fi
        if ! kill -0 "$leader" 2>/dev/null; then
            echo "$drill: $what exited without completing" >&2
            evidence
            exit 1
        fi
        if (( $(date +%s) - started >= 180 )); then
            teardown_group "$leader"
            echo "$drill: $what did not finish within 180s" >&2
            evidence
            exit 1
        fi
        sleep 2
    done
}

# Step zero, on the drills that carry a stick: the hardware report.
# This is the boot a person runs first on a machine they do not know,
# so the drill runs it first too. The guest boots liken.report with a
# copy of the stick attached over USB, exactly the composition a real
# report boot sees: the report must load the stick's own controller
# driver (usb-storage) to write the proposal, and must then keep both
# that driver and the stick's disk out of the proposal itself.
if [[ -n "$REPORT_STICK" ]]; then
    echo "$drill: booting the hardware report"
    cp "$REPORT_STICK" guests/node-1/stick.img
    setsid make run NODE=node-1 BOOT=kernel FIRMWARE="$FIRMWARE" HARDWARE="$HARDWARE" \
        INITRD="$INSTALL_CPIO" LIKEN_BOOT_ARGS=liken.report \
        QEMU_EXIT_ON_REBOOT=-no-reboot \
        QEMU_EXTRA="-device qemu-xhci -drive file=guests/node-1/stick.img,format=raw,if=none,id=stick -device usb-storage,drive=stick" \
        CONSOLE="file:$REPORT_LOG" \
        </dev/null >guests/node-1/qemu.log 2>&1 &
    watch_hold $! "$REPORT_LOG" \
        "this report was written to the stick" \
        "to the stick FAILED" \
        "the report boot"

    # The proposal's proof, read straight from the stick image. The
    # drill searches the raw image rather than mounting it, because a
    # mount needs root and the FAT clusters of one small, fresh file
    # are contiguous in practice. Every marker is a concrete observed
    # value that exists nowhere else on the stick: a bare phrase from
    # the proposal's own template would also match the template string
    # inside the liken binaries the stick carries, and a plain module
    # name would also match the deployment layer.
    proposal_carries() {
        grep -a -q -- "$1" guests/node-1/stick.img || {
            echo "$drill: the proposal on the stick is missing \"$1\"" >&2
            evidence
            exit 1
        }
    }
    proposal_carries "): e1000"      # the NIC evidence names the driver
    proposal_carries "MAC 52:54:00:" # the interface evidence carries real MACs
    proposal_carries "#   /dev/sda"  # the disk evidence lists the AHCI disks
    proposal_carries "(/dev/sdd is the installation stick" # named in evidence, not proposed
    for stickism in "): usb-storage" "- usb-storage" "): uas" "- uas" "device: /dev/sdd"; do
        if grep -a -q -- "$stickism" guests/node-1/stick.img; then
            echo "$drill: the proposal put the installation stick in the manifest (\"$stickism\")" >&2
            evidence
            exit 1
        fi
    done
    echo "$drill: the report wrote a sound proposal to the stick"
fi

# Step one: the install.
echo "$drill: installing node-1 ($FIRMWARE, $HARDWARE)"
setsid make install NODE=node-1 FIRMWARE="$FIRMWARE" HARDWARE="$HARDWARE" \
    INSTALL_CPIO="$INSTALL_CPIO" \
    CONSOLE="file:$INSTALL_LOG" \
    </dev/null >guests/node-1/qemu.log 2>&1 &
watch_hold $! "$INSTALL_LOG" \
    "liken: installed to slot" \
    "liken: install failed" \
    "the install boot"
echo "$drill: install complete; booting the installed disk"

# Step two: boot the disk that the installer just wrote. The boot
# runs in the background, in its own session (setsid), so that one
# signal can later end the whole process tree: make, the shell that
# make spawns, and QEMU. The guest's serial console goes to the
# console log. make's own chatter and QEMU's own chatter go to a
# separate file, because QEMU owns the console file, and a second
# writer would corrupt it.
setsid make run NODE=node-1 BOOT=disk FIRMWARE="$FIRMWARE" HARDWARE="$HARDWARE" \
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
