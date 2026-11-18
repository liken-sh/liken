// The scan program runs in the privileged pods that firmware-operator
// starts on a machine. With no argument it scans: it finds the
// machine's firmware devices and the releases that one catalog offers
// for them. With rehearse or apply it runs a rehearsal or an apply. Each
// mode prints its result as the last line of stdout, as JSON, and the
// operator reads that line from the pod's log. An apply also prints a
// progress line to stdout when its prepare passes.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

const (
	// scanTimeout limits a scan, a rehearsal, and the prepare of an
	// apply. One that runs longer is taken to be stuck, and its failed
	// result lets the operator report it.
	scanTimeout = 10 * time.Minute

	workDir = "/run/scan"

	// A rehearsal reads the machine's /sys/firmware at hostFirmware,
	// mounted read-only, and keeps its downloads and fwupd's state in
	// rehearsalDir, which is in memory. An apply mounts hostFirmware
	// read-write and runs the same rehearsal before it writes.
	hostFirmware = "/host/firmware"
	rehearsalDir = "/run/rehearsal"
	// firmwareCopy is where each pod copies the machine's firmware
	// tables for fwupd. fwupd derives a capsule device's ID from the
	// path of its ESRT entry, so every pod copies to the same path, and
	// a rehearsal finds the device ID that the scan recorded
	// (firmwareCopyPath in rehearsalpod.go).
	firmwareCopy = "/run/fw"

	// nextESPPath is where the operator mounts the EFI system partition
	// that the machine boots from next, when there is one (nextESPPath
	// in rehearsalpod.go).
	nextESPPath = "/run/esp-next"

	// signalDir is the downward API volume of an apply's annotations
	// (signal.go). An apply creates preparedMarker when its prepare
	// passes, and the pod's readiness probe checks for it.
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
	// The readiness probe of an apply pod exits 0 when the prepare has
	// passed, and 1 while it runs or after it failed.
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

func rehearseMain() {
	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	defer cancel()

	r := rehearserIn(rehearserFromEnv(os.Getenv))
	if err := emit(os.Stdout, r.rehearse(ctx)); err != nil {
		fmt.Fprintln(os.Stderr, "writing the result:", err)
		os.Exit(1)
	}
}

// applyMain runs an apply (apply.go): a rehearsal, a wait for the
// operator's signal (signal.go), and then an append to a UEFI variable
// or a capsule install. Its context has no timeout, because the
// machine's reboot turn can come much later.
func applyMain() {
	a := applierFromEnv(os.Getenv)
	a.rehearser, a.store, a.marker = rehearserIn(a.rehearser), osVariableStore(), preparedMarker
	// A capsule install runs fwupd on the machine's own /sys/firmware,
	// mounted read-write, and on the next boot's EFI system partition.
	// fwupd keeps its own state in the pod.
	a.installer = execFwupdtool(nextESPPath, "FWUPD_SYSFSFWDIR="+hostFirmware, "FWUPD_LOCALSTATEDIR="+rehearsalDir+"/machine")
	a.signal = func(ctx context.Context) error { return awaitSignal(ctx, signalDir, inotifyWatch) }
	if err := emit(os.Stdout, a.apply(context.Background(), os.Stdout)); err != nil {
		fmt.Fprintln(os.Stderr, "writing the result:", err)
		os.Exit(1)
	}
}

// rehearserIn sets a rehearser's paths. fwupd reads the firmware copy
// and keeps its state under rehearsalDir, so a rehearsed install changes
// nothing on the machine.
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
