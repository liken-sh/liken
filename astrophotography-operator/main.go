// astrophotography-operator images the targets a person declares, on
// the telescopes that observatory-operator controls. It reads the
// Targets and Solvers of the astrophotography.liken.sh group in every
// namespace, and the Reservations of the observatory.liken.sh group
// that name it as their holder.
//
// One binary serves every program of the component, and its first
// argument selects the program. With no argument it runs the operator.
// promote runs the container of a promotion Job, and modes.go lists the
// modes of the operator's other pods: the solver and the worker.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/astrophotography-operator/promote"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// The pod's name and namespace, set from the downward API in
// deploy/operator.yaml. The namespace locates the Lease, and the pod
// name is the identity that holds it.
const (
	podNameVariable      = "POD_NAME"
	podNamespaceVariable = "POD_NAMESPACE"
)

// metricsAddressVariable sets the host:port where /metrics listens.
// An empty value turns the listener off.
const metricsAddressVariable = "ASTROPHOTOGRAPHY_METRICS_ADDRESS"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "promote" {
		os.Exit(promote.Main(os.Args[2:], os.Stderr))
	}
	if len(os.Args) > 1 {
		run, known := modes[os.Args[1]]
		if !known {
			fmt.Fprintf(os.Stderr, "%s: %q is not a mode, and the operator runs when no argument is given\n", managedBy, os.Args[1])
			os.Exit(2)
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
		err := run(ctx, os.Args[2:], os.Stderr)
		stop()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s %s: %v\n", managedBy, os.Args[1], err)
			os.Exit(1)
		}
		return
	}
	if err := operate(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", managedBy, err)
		os.Exit(1)
	}
}

// operate builds the in-cluster clients and runs process.operate in
// leader.go until SIGTERM.
func operate() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	namespace, pod := os.Getenv(podNamespaceVariable), os.Getenv(podNameVariable)
	if namespace == "" || pod == "" {
		return fmt.Errorf("%s and %s are not set, and the operator needs them to name its Lease", podNamespaceVariable, podNameVariable)
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
		// On SIGTERM the client stops waiting out a 429 but lets a sent
		// request finish, so no write is in flight when the Lease is
		// released.
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
