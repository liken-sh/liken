// firmware-operator writes firmware into the USB devices of a liken
// cluster that hold none of their own, and finds the firmware that each
// machine's devices keep. A Firmware with upload names the devices it
// matches with a CEL selector, and the image and command that load
// them. When a matched device appears in a machine's ResourceSlice, the
// operator starts one short pod on that machine, with a claim on that
// one device, and writes the result into the Firmware's status. A
// FirmwareCatalog names a source of firmware releases, such as LVFS. The
// operator scans each machine against it with fwupd in a short pod, and
// keeps one discovered Firmware with update for each catalog component
// it finds a release of, with the releases the catalog offers.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// The downward API sets these. The operator starts the upload pods in
// its own namespace, holds its Lease in that namespace, and names its
// pod in the Lease's holder.
const (
	podNameVariable      = "POD_NAME"
	podNamespaceVariable = "POD_NAMESPACE"
)

func main() {
	if err := operate(); err != nil {
		fmt.Fprintf(os.Stderr, "firmware-operator: %v\n", err)
		os.Exit(1)
	}
}

// operate builds the in-cluster clients and runs the operator's
// process (leader.go). A SIGTERM from the kubelet ends the process
// after the rule in flight, and the process then releases the Lease.
func operate() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	scanImage := flag.String("scan-image", "", "the image of the scan pods; empty means the operator's own image with -scan added, at its tag")
	interval := flag.Duration("catalog-interval", time.Hour, "the time between two reads of a FirmwareCatalog's jcat")
	flag.Parse()

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
	image, err := resolveScanImage(client.WithWaitContext(ctx), *scanImage, namespace, name)
	if err != nil {
		return err
	}
	return process{
		client:    client.WithWaitContext(ctx),
		watcher:   watcher,
		lease:     lease,
		pod:       name,
		namespace: namespace,
		// A read of a catalog runs inside the pass, so the timeout bounds
		// how long one slow download holds the pass. The transport is
		// Go's default, which verifies LVFS with the CA bundle that the
		// image copies from the trust component.
		discovery: discoverySettings{scanImage: image, interval: *interval, http: &http.Client{Timeout: 2 * time.Minute}},
		report:    func(line string) { fmt.Fprintf(os.Stderr, "firmware-operator: %s\n", line) },
		exit:      os.Exit,
	}.operate(ctx)
}
