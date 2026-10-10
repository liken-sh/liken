// people-operator writes each Person's picture into its status. It
// reads the picture that spec.avatar names, bakes one small thumbnail,
// and writes it to status.thumbnail, so every screen draws the same
// face with no work of its own.
//
// The same binary is the baker pod's program: `people-operator bake
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

// The downward API sets these. The operator reads its own pod for the
// image a baker pod runs, starts nfs:// baker pods in its own
// namespace, holds its Lease in that namespace, and names its pod in
// the Lease's holder.
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
// process (leader.go). A SIGTERM from the kubelet ends the process
// after the Person in flight, and the process then releases the Lease.
func operate() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	name, namespace := os.Getenv(podNameVariable), os.Getenv(podNamespaceVariable)
	if name == "" || namespace == "" {
		return fmt.Errorf("%s and %s must be set", podNameVariable, podNamespaceVariable)
	}
	client, err := apiclient.InCluster(apiclient.InClusterOptions{Timeout: election.RequestTimeout})
	if err != nil {
		return err
	}
	watcher, err := informer.InCluster()
	if err != nil {
		return err
	}
	lease, err := rest.InClusterConfig()
	if err != nil {
		return err
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
