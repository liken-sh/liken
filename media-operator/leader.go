//go:build !pod

package main

// Only one copy of the operator may reconcile at a time, because two
// copies can create two pods for one Play, delete each other's pods, or
// overwrite each other's status. A rolling update, a network partition,
// or more than one replica can run a second copy. So the copies compete
// through kubernetes/election for a Lease named media-operator in the
// operator's namespace. Only the copy that holds the Lease opens the
// bus session and reconciles. During a rolling update the new pod waits
// until the old one releases the Lease.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/events"
)

// leaseName is the name of the Lease that the copies compete for.
const leaseName = "media-operator"

// requestTimeout is the timeout of each request of the operator's
// client, so the client gives up on a write that the guard allowed
// before another copy can take the Lease. The API server can still
// commit that write later (election.RequestTimeout).
const requestTimeout = election.RequestTimeout

type leadership struct {
	*election.Lead
	// report writes one line to the operator's log.
	report func(line string)
}

// prefixed returns a function that adds the operator's log prefix to a
// line before it calls write, because the election writes its lines
// with no prefix.
func prefixed(write func(string)) func(string) {
	return func(line string) { write("media.liken.sh: " + line) }
}

func leaseOptions(namespace, pod string, report func(string)) election.Options {
	return election.Options{Name: leaseName, Namespace: namespace, Pod: pod, Report: report}
}

// guard returns the client with the election's write guard, which
// refuses a write unless this copy holds a current Lease. The guard
// also refuses every write after stepDown, which stops a write from a
// timer that started before the bus session ended, such as a delayed
// volume change (volumeengine.go).
func (l *leadership) guard(client *apiclient.Client) *apiclient.Client {
	return client.WithWriteGuard(l.MayWrite)
}

// stepDown releases the Lease after the last pass. It first calls
// quiet to stop the bus session, because a key press or a status report
// on the bus can write to the API. Then it shuts down the Event
// recorder, which writes the last pass's Events from its own goroutine.
// Then it releases the Lease.
//
// When the session or the recorder does not stop in time, stepDown
// keeps the Lease, and the process exits holding it. Another copy takes
// the Lease only after it expires, which is later than requestTimeout
// lets any write still in flight run.
func (l *leadership) stepDown(quiet func() bool, recorder *events.Recorder) {
	if !quiet() {
		l.report("the bus session did not stop, so the Lease is left to expire")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), events.ShutdownTimeout)
	defer cancel()
	if err := recorder.Shutdown(ctx); err != nil {
		l.report(err.Error() + ", so the Lease is left to expire")
		return
	}
	l.StepDown()
}

// lead blocks until this process holds the Lease. It exits the process
// with status 0 if stop ends first, and exits it if the Lease is lost
// later.
func lead(stop context.Context) *leadership {
	namespace, pod := os.Getenv(podNamespaceVariable), os.Getenv(podNameVariable)
	if namespace == "" || pod == "" {
		fmt.Fprintf(os.Stderr, "%s and %s are unset, so the operator cannot name its Lease\n",
			podNamespaceVariable, podNameVariable)
		os.Exit(1)
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the in-cluster config for the Lease: %v\n", err)
		os.Exit(1)
	}
	report := prefixed(func(line string) { fmt.Println(line) })
	l, err := election.Start(stop, config, leaseOptions(namespace, pod, report))
	if errors.Is(err, election.ErrStopped) {
		os.Exit(0)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "running the leader election: %v\n", err)
		os.Exit(1)
	}
	return &leadership{Lead: l, report: report}
}
