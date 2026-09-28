//go:build node

package main

// This file replaces leader.go in the node build, the binary that the
// cec DaemonSet runs on each node with a CEC adapter. leader.go links
// client-go's leader election, which links client-go's typed clientset
// and its scheme. That more than doubles the binary and the memory the
// node workload takes at start, and the node workload elects nothing:
// each pod holds the one adapter on its own node. The node build sets
// the build tag node, so leader.go, the election, and the typed
// clientset stay out of it. The node workload's watches link only
// client-go's reflector, dynamic client, and rest (watch.go). The
// Deployment runs the full build.

import (
	"context"
	"fmt"
	"os"
)

// leadership has no election in the node build.
type leadership struct{}

// lead refuses to run the Deployment's operator from the node build,
// because the node build cannot take the Lease that keeps the operator
// to one acting copy.
func lead(*Client, settings) *leadership {
	fmt.Fprintf(os.Stderr, "%s runs the node workload only; the operator runs from %s\n",
		nodeBinary, operatorBinary)
	os.Exit(1)
	return nil
}

func (*leadership) actWhileLeading(context.Context, func() error) (bool, error) {
	return false, nil
}
