//go:build !pod

package main

// The operator is a cluster singleton. It creates and deletes pods and
// writes every Play's status, so two copies that reconcile at the same
// time race: both create a pod for one Play, or one deletes the pod the
// other just made, or two status writes overwrite each other. A rollout,
// a node partition, and a replica count above one each run a second
// copy, so the operator keeps one acting copy with the election of
// kubernetes/election, on the coordination.k8s.io Lease named
// media-operator in its own namespace.
//
// Only the copy that holds the Lease opens a bus session and
// reconciles. Every other copy waits, so a rollout starts the new pod
// beside the old one, and the new pod takes over when the old one
// releases the Lease. The package gives the timings, the release, the
// exit on a lost Lease, and the write guard that the operator's client
// asks before each write.

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

// leaseName names the Lease the operator's copies compete for, in the
// operator's own namespace.
const leaseName = "media-operator"

// requestTimeout limits each request of the operator's client, so the
// client gives up on a write the guard allowed before another copy can
// take the Lease. The API server can still commit that write later
// (election.RequestTimeout).
const requestTimeout = election.RequestTimeout

// leadership is this process's part in the election.
type leadership struct {
	*election.Lead
	// report writes one line of the election to the operator's log.
	report func(line string)
}

// prefixed adds the operator's prefix to each line and passes the line
// to write. The election's own lines carry no prefix.
func prefixed(write func(string)) func(string) {
	return func(line string) { write("media.liken.sh: " + line) }
}

// leaseOptions names the operator's Lease for the pod.
func leaseOptions(namespace, pod string, report func(string)) election.Options {
	return election.Options{Name: leaseName, Namespace: namespace, Pod: pod, Report: report}
}

// guard answers the operator's client with the election's write guard,
// so a copy that cannot show it holds a current Lease sends no write.
// The guard refuses every write after stepDown too, which stops a write
// that a timer started before the bus session ended, such as a pending
// volume ask (volumeengine.go).
func (l *leadership) guard(client *apiclient.Client) *apiclient.Client {
	return client.WithWriteGuard(l.MayWrite)
}

// stepDown ends this process's lead on a shutdown. The caller calls it
// after the last pass. It runs quiet first, which stops the bus session,
// because a press or a report on the bus can write to the API. Then it
// shuts the Event recorder down, because the recorder writes the Events
// of the last pass from its own goroutine. Then it releases the Lease.
// When the session or the recorder does not stop in time, the Lease is
// not released: the process exits holding it, and a waiting copy takes
// it when it expires, after any write still in flight can land.
func (l *leadership) stepDown(quiet func() bool, recorder *events.Recorder) {
	if !quiet() {
		l.report("the bus session did not stop; leaving the Lease to expire")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), events.ShutdownTimeout)
	defer cancel()
	if err := recorder.Shutdown(ctx); err != nil {
		l.report(err.Error() + "; leaving the Lease to expire")
		return
	}
	l.StepDown()
}

// lead blocks until this process holds the Lease. It exits the process
// when stop ends first, and whenever the Lease is lost after that.
func lead(stop context.Context) *leadership {
	namespace, pod := os.Getenv(podNamespaceVariable), os.Getenv(podNameVariable)
	if namespace == "" || pod == "" {
		fmt.Fprintf(os.Stderr, "%s and %s are unset, so the operator cannot name its Lease\n",
			podNamespaceVariable, podNameVariable)
		os.Exit(1)
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "in-cluster config for the Lease: %v\n", err)
		os.Exit(1)
	}
	report := prefixed(func(line string) { fmt.Println(line) })
	l, err := election.Start(stop, config, leaseOptions(namespace, pod, report))
	if errors.Is(err, election.ErrStopped) {
		os.Exit(0)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "leader election: %v\n", err)
		os.Exit(1)
	}
	return &leadership{Lead: l, report: report}
}
