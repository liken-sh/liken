// astrophotography-operator images the targets a person declares, on
// the telescopes that observatory-operator controls. It reads the
// Targets and Solvers of the astrophotography.liken.sh group in every
// namespace, and the Reservations of the observatory.liken.sh group
// that name it as their holder.
//
// One binary holds every program of the component, and its first
// argument names the program. With no argument it runs the operator.
// Plans 02 and 03 add the modes that run in the operator's own pods:
// promote, worker, and solver.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// The pod's name and namespace, from the downward API in
// deploy/operator.yaml. They name the Lease and the identity that
// holds it.
const (
	podNameVariable      = "POD_NAME"
	podNamespaceVariable = "POD_NAMESPACE"
)

// metricsAddressVariable names where /metrics listens, as host:port.
// Root plan 65 gives every process on the cluster network port 9200,
// and deploy/operator.yaml sets it. An empty value turns the listener
// off.
const metricsAddressVariable = "ASTROPHOTOGRAPHY_METRICS_ADDRESS"

func main() {
	if len(os.Args) > 1 {
		fmt.Fprintf(os.Stderr, "%s: no mode %q; run with no argument for the operator\n", managedBy, os.Args[1])
		os.Exit(2)
	}
	if err := operate(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", managedBy, err)
		os.Exit(1)
	}
}

// operate builds the in-cluster clients, which send nothing yet, and
// runs the operator's process (leader.go). A SIGTERM ends the process,
// and the process releases the Lease after its last write.
func operate() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	namespace, pod := os.Getenv(podNamespaceVariable), os.Getenv(podNameVariable)
	if namespace == "" || pod == "" {
		return fmt.Errorf("%s and %s are unset, so the operator cannot name its Lease", podNamespaceVariable, podNameVariable)
	}
	lease, err := rest.InClusterConfig()
	if err != nil {
		return err
	}
	client, err := apiclient.InCluster(apiclient.InClusterOptions{Timeout: election.RequestTimeout})
	if err != nil {
		return err
	}
	watcher, err := informer.InCluster()
	if err != nil {
		return err
	}
	return process{
		// The stop ends the wait after a 429, and does not cut off a
		// request in flight, so a write either returns before the
		// release or was never sent.
		client:         client.WithWaitContext(ctx),
		watcher:        watcher,
		lease:          lease,
		pod:            pod,
		namespace:      namespace,
		metricsAddress: os.Getenv(metricsAddressVariable),
		report:         func(line string) { fmt.Fprintf(os.Stderr, "%s: %s\n", managedBy, line) },
		logs:           os.Stderr,
		exit:           os.Exit,
	}.operate(ctx)
}
