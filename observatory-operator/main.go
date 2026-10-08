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

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

func main() {
	if err := operate(); err != nil {
		fmt.Fprintf(os.Stderr, "observatory-operator: %v\n", err)
		os.Exit(1)
	}
}

func operate() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		return err
	}
	watcher, err := informer.InCluster()
	if err != nil {
		return err
	}
	n := newNamespaces(client, nil)
	n.recorder = events.New(ctx, client, managedBy, events.Options{Log: n.logs})
	n.run(ctx, func(ctx context.Context, changed *bell) *stores { return startWatches(ctx, watcher, changed) })
	return nil
}
