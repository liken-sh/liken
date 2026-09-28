//go:build pod

package main

// This file replaces leader.go in the pod build, the binary that runs
// every role but the operator: the command sidecar in every playback
// pod, the reader in every Remote's pod, the player shim, and the api. leader.go links client-go's
// leader election, which links client-go's typed clientset and its
// scheme. That more than doubles the binary and the memory each role
// takes at start, and those roles never elect anything. The pod build
// sets the build tag pod, so leader.go and client-go stay out of it.
// The operator runs from the full build.

import (
	"context"
	"fmt"
	"os"
)

// leadership has no election in the pod build.
type leadership struct{}

// lead refuses to run the operator from the pod build, because the pod
// build cannot take the Lease that keeps the operator to one acting
// copy.
func lead(context.Context) *leadership {
	fmt.Fprintf(os.Stderr, "%s runs the pod roles only; the operator runs from %s\n",
		podBinary, operatorBinary)
	os.Exit(1)
	return nil
}

func (*leadership) stepDown(func() bool) {}
