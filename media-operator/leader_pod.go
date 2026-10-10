//go:build pod

package main

// This file replaces leader.go in the pod build, the binary that runs
// every role but the operator: the command sidecar in every playback
// pod, the reader in every Remote's pod, the player shim, and the api.
// leader.go links kubernetes/election, which links client-go's leader
// election, its typed clientset, and its scheme. That more than doubles
// the binary and the memory each role takes at start, and those roles
// never elect anything. The pod build sets the build tag pod, so
// leader.go, the election, and the typed clientset stay out of it. The
// api's watches link only client-go's reflector and dynamic client
// (watch.go). The operator runs from the full build.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
)

// requestTimeout takes apiclient's default in the pod build, where lead
// exits before the operator's client sends anything.
const requestTimeout = 0

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

// errNoLease refuses every write, because the pod build holds no Lease.
var errNoLease = errors.New("the pod build holds no Lease")

func (*leadership) guard(client *apiclient.Client) *apiclient.Client {
	return client.WithWriteGuard(func() error { return errNoLease })
}

func (*leadership) stepDown(func() bool, *events.Recorder) {}
