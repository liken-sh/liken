//go:build pod

package main

// This file replaces operatorprocess.go in the pod build, the binary
// that runs every role but the operator: the scanner and the enricher
// in each library Job, the confirmer and the reporter in each catalog
// pod, the progress agent, and the Jellyfin roles. The operator links
// client-go for its watches and its Lease, and the Lease links
// client-go's typed clientset and its scheme. That more than doubles
// the binary and the memory each role takes at start, and those roles
// watch nothing and elect nothing. The pod build sets the build tag
// pod, so operatorprocess.go, watch.go, leader.go, and client-go stay
// out of it. The operator runs from the full build.

import "fmt"

// operate refuses to run the operator from the pod build, because the
// pod build cannot take the Lease that keeps the operator to one acting
// copy.
func operate() error {
	return fmt.Errorf("%s runs the pod roles only; the operator runs from %s", podBinary, operatorBinary)
}
