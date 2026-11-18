// observatory-operator runs an observatory's hardware through INDI. It
// reads the resources of the observatory.liken.sh group in every
// namespace, and while a Reservation is active it runs the
// reservation's telescope: one pod for each device, one INDI server
// for the telescope and one for the observatory, and the steps that
// connect, configure, and prepare each device, and secure it again at
// the end.
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

// The operator pod's name and namespace, set from the downward API in
// deploy/operator.yaml. The Lease lives in the namespace, and the pod
// name is the identity that holds it.
const (
	podNameVariable      = "POD_NAME"
	podNamespaceVariable = "POD_NAMESPACE"
)

// metricsAddressVariable is the environment variable that holds the
// host:port where /metrics listens. deploy/operator.yaml sets it to
// port 9200, the metrics port of every process on the cluster network.
// An empty value turns the listener off.
const metricsAddressVariable = "OBSERVATORY_METRICS_ADDRESS"

func main() {
	if err := operate(); err != nil {
		fmt.Fprintf(os.Stderr, "observatory-operator: %v\n", err)
		os.Exit(1)
	}
}

// operate runs the operator until SIGTERM or an interrupt. It creates
// the clients, which send no request yet. Then it waits for the Lease,
// and opens watches and writes only while it holds the Lease
// (leader.go). On a signal, the work of each namespace stops, and the
// Lease is released after the last writer returns.
func operate() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	namespace, pod := os.Getenv(podNamespaceVariable), os.Getenv(podNameVariable)
	if namespace == "" || pod == "" {
		return fmt.Errorf("%s and %s are not set, so the operator has no holder identity or namespace for its Lease", podNamespaceVariable, podNameVariable)
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("reading the in-cluster configuration: %w", err)
	}
	// Canceling ctx ends the client's wait to retry after a 429. It
	// does not cancel a request already sent, so each write has either
	// returned before the Lease is released or was never sent.
	client, err := apiclient.InCluster(apiclient.InClusterOptions{Timeout: election.RequestTimeout})
	if err != nil {
		return fmt.Errorf("creating the API client: %w", err)
	}
	client = client.WithWaitContext(ctx)
	watcher, err := informer.InCluster()
	if err != nil {
		return fmt.Errorf("creating the watch client: %w", err)
	}
	options := election.Options{
		Name: leaseName, Namespace: namespace, Pod: pod,
		Report: func(line string) { fmt.Fprintf(os.Stderr, "observatory-operator: %s\n", line) },
	}
	return actWhileLeading(ctx, config, options, func(mayWrite func() error) error {
		n := leadingNamespaces(client, mayWrite, nil, nil, "")
		// Only the pod that holds the Lease serves metrics, because only
		// it has series to serve. A listener that fails to open is
		// logged, and the operator keeps working.
		if _, err := n.metrics.serve(ctx, os.Getenv(metricsAddressVariable)); err != nil {
			fmt.Fprintf(os.Stderr, "observatory-operator: not serving metrics: %v\n", err)
		}
		n.run(ctx, func(ctx context.Context, changed *changeNotifier) *stores {
			return startWatches(ctx, watcher, changed, n.metrics)
		})
		return n.finish()
	})
}
