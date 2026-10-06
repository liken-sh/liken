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

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// The downward API sets these, so the operator can read its own pod
// for the image a baker pod runs, and start nfs:// baker pods in its
// own namespace.
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

func operate() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	name, namespace := os.Getenv(podNameVariable), os.Getenv(podNamespaceVariable)
	if name == "" || namespace == "" {
		return fmt.Errorf("%s and %s must be set", podNameVariable, podNamespaceVariable)
	}
	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		return err
	}
	client = client.WithContext(ctx)
	watcher, err := informer.InCluster()
	if err != nil {
		return err
	}
	image, err := ownImage(client, namespace, name)
	if err != nil {
		return err
	}
	bakers := &bakers{client: client, image: image, namespace: namespace}
	recorder := events.New(ctx, client, component, events.Options{Instance: name})
	newOperator(client, http.DefaultTransport, bakers, recorder).run(ctx, watcher)
	return nil
}
