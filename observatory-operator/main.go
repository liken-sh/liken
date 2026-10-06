// observatory-operator runs an observatory's hardware through INDI. It
// reads the resources of the observatory.liken.sh group, and while a
// Reservation is active it runs the reservation's telescope: one pod
// for each device, one INDI server for the telescope and one for the
// observatory, and the steps that connect, configure, and prepare each
// device, and secure it again at the end.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// podNamespaceVariable names the operator's namespace. The Deployment
// sets it from the downward API, and the operator watches that one
// namespace.
const podNamespaceVariable = "POD_NAMESPACE"

func main() {
	if err := operate(); err != nil {
		fmt.Fprintf(os.Stderr, "observatory-operator: %v\n", err)
		os.Exit(1)
	}
}

func operate() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	namespace := os.Getenv(podNamespaceVariable)
	if namespace == "" {
		return fmt.Errorf("%s must be set", podNamespaceVariable)
	}
	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		return err
	}
	watcher, err := informer.InCluster()
	if err != nil {
		return err
	}
	o := newOperator(namespace, client, nil)
	o.run(ctx, func(ctx context.Context) *stores { return startWatches(ctx, watcher, namespace, o.structure) })
	return nil
}
