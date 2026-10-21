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
	// rehearsalDir, a directory in memory.
	hostFirmware = "/host/firmware"
	rehearsalDir = "/run/rehearsal"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "rehearse" {
		rehearseMain()
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

// rehearseMain runs a rehearsal (rehearse.go). fwupd reads the copy in
// rehearsalDir and keeps its own state there too, so nothing of the
// install reaches the machine.
func rehearseMain() {
	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	defer cancel()

	copyDir := rehearsalDir + "/fw"
	r := rehearserFromEnv(os.Getenv)
	r.scanner = scanner{
		cfg:    configFromEnv(os.Getenv),
		client: http.DefaultClient,
		fwupdtool: execFwupdtool(rehearsalDir+"/esp",
			"FWUPD_SYSFSFWDIR="+copyDir, "FWUPD_LOCALSTATEDIR="+rehearsalDir+"/var"),
		remotesDir: "/etc/fwupd/remotes.d",
		workDir:    rehearsalDir + "/catalog",
	}
	r.hostDir, r.copyDir, r.espDir, r.free = hostFirmware, copyDir, rehearsalDir+"/esp", freeBytes
	if err := emit(os.Stdout, r.rehearse(ctx)); err != nil {
		fmt.Fprintln(os.Stderr, "writing the result:", err)
		os.Exit(1)
	}
}
