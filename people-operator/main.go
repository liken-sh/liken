// people-operator reads the picture at each Person's spec.avatar,
// scales it to a small thumbnail, and writes the thumbnail to
// status.thumbnail. Screens then draw the thumbnail and do not fetch or
// decode pictures themselves.
//
// The same binary is the baker pod's command: `people-operator bake
// <path> <colour>` bakes one file and prints the result (bake.go).
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// The Deployment sets these from the downward API. The operator reads
// its own pod to find the image for baker pods, and creates nfs://
// baker pods and its Lease in its own namespace. The election uses the
// pod name as the start of this process's holder identity in the Lease.
const (
	podNameVariable      = "POD_NAME"
	podNamespaceVariable = "POD_NAMESPACE"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "bake" {
		os.Exit(runBake(os.Stdout, os.Stderr, os.Args[2:]))
	}
	if err := operate(); err != nil {
		fmt.Fprintf(os.Stderr, "people-operator: %v\n", err)
		os.Exit(1)
	}
}

// operate builds the in-cluster clients and runs the operator's
// process (leader.go). On SIGTERM the process finishes the Person in
// progress and releases the Lease.
func operate() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	name, namespace := os.Getenv(podNameVariable), os.Getenv(podNamespaceVariable)
	if name == "" || namespace == "" {
		return fmt.Errorf("%s and %s must be set", podNameVariable, podNamespaceVariable)
	}
	client, err := apiclient.InCluster(apiclient.InClusterOptions{Timeout: election.RequestTimeout})
	if err != nil {
		return fmt.Errorf("building the API client: %w", err)
	}
	watcher, err := informer.InCluster()
	if err != nil {
		return fmt.Errorf("building the watch client: %w", err)
	}
	lease, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("reading the in-cluster configuration for the Lease: %w", err)
	}
	return process{
		client:    client.WithWaitContext(ctx),
		watcher:   watcher,
		lease:     lease,
		transport: http.DefaultTransport,
		pod:       name,
		namespace: namespace,
		report:    func(line string) { fmt.Fprintf(os.Stderr, "people-operator: %s\n", line) },
		exit:      os.Exit,
	}.operate(ctx)
}
