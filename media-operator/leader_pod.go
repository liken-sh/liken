//go:build pod

package main

// This file replaces leader.go in the pod build, the binary for every
// role except the operator: the command sidecar in each playback pod,
// the reader in each Remote's pod, the player shim, and the api.
// leader.go imports client-go's leader election, typed clientset, and
// scheme through kubernetes/election. These roles never run an election,
// and a copy of them runs in every playback pod and every Remote's pod,
// so the build tag pod leaves those packages out to make the binary
// smaller and lower its memory at start. The api's watches therefore
// use only client-go's reflector and dynamic client (watch.go).

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
)

// requestTimeout 0 selects apiclient's default timeout. In the pod build
// the operator's client sends only the reads that operate makes before
// lead exits.
const requestTimeout = 0

type leadership struct{}

// lead exits with an error, because the pod build cannot take the Lease
// that limits the operator to one active copy.
func lead(context.Context) *leadership {
	fmt.Fprintf(os.Stderr, "%s runs the pod roles only. Run the operator from %s\n",
		podBinary, operatorBinary)
	os.Exit(1)
	return nil
}

var errNoLease = errors.New("writes are refused because the pod build does not hold the Lease")

func (*leadership) guard(client *apiclient.Client) *apiclient.Client {
	return client.WithWriteGuard(func() error { return errNoLease })
}

func (*leadership) stepDown(func() bool, *events.Recorder) {}
