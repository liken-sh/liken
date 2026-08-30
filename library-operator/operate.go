package main

// The operator reconciles Library resources into scanner pods and
// media browsers. In this plan it starts, proves that it can reach
// the API server, and holds the Deployment open with nothing to
// reconcile.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// operate returns its failure instead of exiting, so a test runs the
// whole setup. main is the only place that ends the process.
func operate() error {
	client, err := InClusterClient()
	if err != nil {
		return fmt.Errorf("in-cluster config: %w", err)
	}

	// The kubelet stops the pod with SIGTERM, and a person who runs
	// the binary by hand stops it with SIGINT. Both end the context,
	// and the process exits with a zero status.
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	return run(stopped, client, os.Stdout)
}

// run is the operator without the process around it, so a test proves
// the connection and the report against an httptest server. The
// context ends on the stop signal, and the report goes to the writer.
func run(stopped context.Context, client *Client, report io.Writer) error {
	// The first request proves that the address, the CA, and the token
	// are all right. If one of them is wrong, the pod fails at once.
	version, err := ServerVersion(client)
	if err != nil {
		return fmt.Errorf("reaching the API server: %w", err)
	}
	fmt.Fprintf(report, "library.liken.sh: connected to Kubernetes %s; no Library to reconcile yet\n", version.GitVersion)

	<-stopped.Done()
	return nil
}
