// The scan program runs in the privileged pod that firmware-operator
// starts on one machine. It answers which firmware devices the machine
// has and which releases one catalog offers for them, and prints the
// answer as one line of JSON on stdout. Everything else it prints goes
// to stderr, so the operator finds the answer as the last line of the
// pod's log.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

const (
	// scanTimeout bounds the whole scan. The downloads and fwupdtool
	// are fast on a working network, so a scan that passes this is
	// stuck, and a Failed result lets the operator report it.
	scanTimeout = 10 * time.Minute

	workDir = "/run/scan"

	// A rehearsal reads the machine's /sys/firmware at hostFirmware,
	// mounted read-only, and keeps its copy and its downloads in
	// rehearsalDir, a directory in memory. An apply mounts the same
	// directory read-write at the same path, and rehearses the same
	// way before it writes.
	hostFirmware = "/host/firmware"
	rehearsalDir = "/run/rehearsal"
	// firmwareCopy is where each pod copies the machine's firmware
	// tables for fwupd. fwupd names a capsule device by the path of its
	// ESRT entry, so the scan, the rehearsal, and the apply's prepare
	// copy to the same path, and the device keeps the ID that the scan
	// recorded (firmwareCopyPath in rehearsalpod.go).
	firmwareCopy = "/run/fw"

	// nextESPPath is where the operator mounts the ESP of the machine's
	// next boot, when it has one (nextESPPath in rehearsalpod.go).
	nextESPPath = "/run/esp-next"

	// An apply reads its own annotations, which carry the operator's
	// signal to write, from the downward API volume at signalDir. It
	// marks its finished prepare with the file preparedMarker, in a
	// directory in memory, which the pod's readiness probe reads.
	signalDir      = "/run/signal"
	preparedMarker = "/run/apply/prepared"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "rehearse" {
		rehearseMain()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "apply" {
		applyMain()
		return
	}
	// The readiness probe of an apply pod: exit 0 when the apply's
	// prepare passed, and 1 while it runs or after it failed.
	if len(os.Args) > 1 && os.Args[1] == "prepared" {
		if !isPrepared(preparedMarker) {
			os.Exit(1)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	defer cancel()

	s := scanner{
		cfg:          configFromEnv(os.Getenv),
		client:       http.DefaultClient,
		fwupdtool:    execFwupdtool(workDir+"/esp", "FWUPD_SYSFSFWDIR="+firmwareCopy),
		firmwareHost: hostFirmware,
		firmwareCopy: firmwareCopy,
		remotesDir:   "/etc/fwupd/remotes.d",
		trustDir:     trustDir,
		pkiDir:       pkiDir,
		workDir:      workDir + "/catalog",
	}
	if err := emit(os.Stdout, s.scan(ctx)); err != nil {
		fmt.Fprintln(os.Stderr, "writing the result:", err)
		os.Exit(1)
	}
}

// rehearseMain runs a rehearsal (rehearse.go).
func rehearseMain() {
	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	defer cancel()

	r := rehearserIn(rehearserFromEnv(os.Getenv))
	if err := emit(os.Stdout, r.rehearse(ctx)); err != nil {
		fmt.Fprintln(os.Stderr, "writing the result:", err)
		os.Exit(1)
	}
}

// applyMain runs an apply (apply.go): a rehearsal like rehearseMain's,
// the wait for the operator's signal (signal.go), and then the write to
// the machine's own variable. apply bounds the rehearsal itself, and the
// wait has no bound.
func applyMain() {
	a := applierFromEnv(os.Getenv)
	a.rehearser, a.store, a.marker = rehearserIn(a.rehearser), osVariableStore(), preparedMarker
	// A capsule's install runs fwupd on the machine itself: its variable
	// store through the read-write mount of /sys/firmware, and the ESP
	// of its next boot. fwupd keeps its own state in the pod.
	a.installer = execFwupdtool(nextESPPath, "FWUPD_SYSFSFWDIR="+hostFirmware, "FWUPD_LOCALSTATEDIR="+rehearsalDir+"/machine")
	a.signal = func(ctx context.Context) error { return awaitSignal(ctx, signalDir, inotifyWatch) }
	if err := emit(os.Stdout, a.apply(context.Background(), os.Stdout)); err != nil {
		fmt.Fprintln(os.Stderr, "writing the result:", err)
		os.Exit(1)
	}
}

// rehearserIn completes a rehearser with the paths of its pod. fwupd
// reads the copy in rehearsalDir and keeps its own state there too, so
// nothing of the install reaches the machine.
func rehearserIn(r rehearser) rehearser {
	copyDir := firmwareCopy
	r.scanner = scanner{
		cfg:    configFromEnv(os.Getenv),
		client: http.DefaultClient,
		fwupdtool: execFwupdtool(rehearsalDir+"/esp",
			"FWUPD_SYSFSFWDIR="+copyDir, "FWUPD_LOCALSTATEDIR="+rehearsalDir+"/var"),
		remotesDir: "/etc/fwupd/remotes.d",
		trustDir:   trustDir,
		pkiDir:     pkiDir,
		workDir:    rehearsalDir + "/catalog",
	}
	r.hostDir, r.copyDir, r.espDir, r.free = hostFirmware, copyDir, rehearsalDir+"/esp", freeBytes
	r.espSource, r.partitionOf = nextESPPath, mountedPartition
	return r
}
