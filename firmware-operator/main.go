// firmware-operator does two jobs for a liken cluster.
//
// It uploads firmware to USB devices that lose their firmware at power
// off. A Firmware's spec.selector picks devices with a CEL expression,
// and its spec.upload sets the image and command that load them. When a
// matching device appears in a machine's ResourceSlice, the operator
// runs a pod on that machine with a claim on the device, and writes the
// result into the Firmware's status.
//
// It also finds and installs firmware updates. A FirmwareCatalog is a
// source of firmware releases, such as LVFS. The operator runs fwupd in
// a pod on each machine to scan the machine against the catalog. For
// each catalog component that has a release for a device, the operator
// creates a discovered Firmware whose spec.update lists the releases.
// When a person approves a release there, the operator rehearses the
// install on each machine, then applies it during a reboot that it
// requests through a Disruption.
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

// The downward API sets these variables. The operator creates its pods
// and its Lease in its own namespace, and uses its pod name as the
// Lease's holder identity.
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

// operate builds the in-cluster clients and runs the operator until
// SIGTERM or an interrupt.
func operate() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	scanImage := flag.String("scan-image", "", "the image of the scan pods. When empty, the operator uses its own image with -scan added to the repository name, at the same tag")
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
		discovery: discoverySettings{scanImage: image, interval: *interval, http: &http.Client{Timeout: 2 * time.Minute}},
		report:    func(line string) { fmt.Fprintf(os.Stderr, "firmware-operator: %s\n", line) },
		exit:      os.Exit,
	}.operate(ctx)
}
