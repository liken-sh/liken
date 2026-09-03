// The library operator is the media library layer of a liken cluster.
// It declares libraries as Kubernetes resources, keeps a catalog of
// what they hold, and draws that catalog on the screens the media
// operator plays to.
//
// One binary, with modes, the way the media operator's one image runs
// in several roles. With no argument it is the operator. The plans
// that add the scanners, the enricher, and the organizer add their
// roles as arguments here.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		// Each pod role is a case here. It runs its role and returns.
		case scanMode:
			runScan()
			return
		case cleanupMode:
			runCleanup()
			return
		case reportMode:
			runReport()
			return
		case factsMode:
			runFacts()
			return
		case enrichMode:
			runEnrich()
			return
		}
	}

	// A failure ends the process on purpose. The kubelet restarts the
	// pod with backoff, and the failure shows in kubectl instead of
	// hiding in a retry loop.
	if err := operate(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
