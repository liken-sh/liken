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
)

func main() {
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
