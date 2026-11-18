//go:build node

package main

// This file replaces leader.go in the node build, the binary that the
// cec DaemonSet runs on each node that has a CEC adapter. leader.go
// imports client-go's leader election, which imports the typed
// clientset and its scheme, and those make the binary and its memory
// use much larger. The node workload needs no election, because each
// pod is the only user of the adapter on its node. Its watches use only
// client-go's reflector, dynamic client, and rest (watch.go). The
// Deployment runs the full build.

import (
	"context"
	"fmt"
	"os"
)

// leadership has no state in the node build, which runs no election.
type leadership struct{}

// lead exits with an error, because the node build has no leader
// election, and without the Lease more than one copy of the operator
// would act at once.
func lead(settings) *leadership {
	fmt.Fprintf(os.Stderr, "%s runs the node workload only, and the operator runs from %s\n",
		nodeBinary, operatorBinary)
	os.Exit(1)
	return nil
}

func (*leadership) actWhileLeading(context.Context, func(*Client) error) (bool, error) {
	return false, nil
}
