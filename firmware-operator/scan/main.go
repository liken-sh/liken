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

	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	defer cancel()

	s := scanner{
		cfg:        configFromEnv(os.Getenv),
		client:     http.DefaultClient,
		mounter:    osMounter(),
		fwupdtool:  execFwupdtool(workDir + "/esp"),
		remotesDir: "/etc/fwupd/remotes.d",
		workDir:    workDir + "/catalog",
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
// and then the write to the machine's own variable.
func applyMain() {
	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	defer cancel()

	a := applierFromEnv(os.Getenv)
	a.rehearser, a.store = rehearserIn(a.rehearser), osVariableStore()
	if err := emit(os.Stdout, a.apply(ctx)); err != nil {
		fmt.Fprintln(os.Stderr, "writing the result:", err)
		os.Exit(1)
	}
}

// rehearserIn completes a rehearser with the paths of its pod. fwupd
// reads the copy in rehearsalDir and keeps its own state there too, so
// nothing of the install reaches the machine.
func rehearserIn(r rehearser) rehearser {
	copyDir := rehearsalDir + "/fw"
	r.scanner = scanner{
		cfg:    configFromEnv(os.Getenv),
		client: http.DefaultClient,
		fwupdtool: execFwupdtool(rehearsalDir+"/esp",
			"FWUPD_SYSFSFWDIR="+copyDir, "FWUPD_LOCALSTATEDIR="+rehearsalDir+"/var"),
		remotesDir: "/etc/fwupd/remotes.d",
		workDir:    rehearsalDir + "/catalog",
	}
	r.hostDir, r.copyDir, r.espDir, r.free = hostFirmware, copyDir, rehearsalDir+"/esp", freeBytes
	return r
}
