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

// The pod's name and namespace, from the downward API in
// deploy/operator.yaml. They name the Lease and the identity that
// holds it.
const (
	podNameVariable      = "POD_NAME"
	podNamespaceVariable = "POD_NAMESPACE"
)

func main() {
	if err := operate(); err != nil {
		fmt.Fprintf(os.Stderr, "observatory-operator: %v\n", err)
		os.Exit(1)
	}
}

// operate builds the clients, which send nothing yet, and then waits
// for the Lease before it opens a watch or writes (leader.go). A
// SIGTERM ends the namespaces' work, and the Lease is released when
// the last writer has returned.
func operate() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	namespace, pod := os.Getenv(podNamespaceVariable), os.Getenv(podNameVariable)
	if namespace == "" || pod == "" {
		return fmt.Errorf("%s and %s are unset, so the operator cannot name its Lease", podNamespaceVariable, podNameVariable)
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}
	// The stop ends the wait after a 429, and does not cut off a
	// request in flight, so a write either returns before the release
	// or was never sent.
	client, err := apiclient.InCluster(apiclient.InClusterOptions{Timeout: election.RequestTimeout})
	if err != nil {
		return err
	}
	client = client.WithWaitContext(ctx)
	watcher, err := informer.InCluster()
	if err != nil {
		return err
	}
	options := election.Options{
		Name: leaseName, Namespace: namespace, Pod: pod,
		Report: func(line string) { fmt.Fprintf(os.Stderr, "observatory-operator: %s\n", line) },
	}
	return actWhileLeading(ctx, config, options, func(mayWrite func() error) error {
		n := leadingNamespaces(client, mayWrite, nil, nil, "")
		n.run(ctx, func(ctx context.Context, changed *bell) *stores { return startWatches(ctx, watcher, changed) })
		return n.finish()
	})
}
