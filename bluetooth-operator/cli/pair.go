package main

// The pair verb, an interactive pairing window over the
// PairingRequest API. It opens a window, which starts the radio's
// discovery, draws the devices the radio reports as they appear, reads
// the one a person approves, and writes that address back the way an
// operator approval does. Ctrl-C closes the window so the radio does
// not scan on with nobody watching.

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/signal"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// The pair verb's flags and its one positional
// argument, the adapter to open the window on.
type pairOptions struct {
	Namespace string
	Adapter   string
	Window    int
	Force     bool
}

// runPair wires the terminal and the cluster to the
// flow. It reads the operator version and warns or refuses on drift,
// resolves the adapter, and runs the flow with stdin feeding the
// picker and Ctrl-C closing the window.
func runPair(ctx context.Context, getter genericclioptions.RESTClientGetter, opts pairOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	config, err := getter.ToRESTConfig()
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return err
	}

	operator, err := operatorVersion(ctx, clientset)
	if err != nil {
		fmt.Fprintf(stderr, "reading the operator version: %v\n", err)
	}
	switch action, message := decideVersionAction(version, operator, ""); action {
	case actionRefuse:
		return fmt.Errorf("%s", message)
	case actionWarn:
		if !opts.Force {
			fmt.Fprintln(stderr, message)
		}
	}

	if opts.Namespace == "" {
		opts.Namespace = operatorNamespace
	}
	if opts.Adapter == "" {
		opts.Adapter, err = soleAdapter(ctx, dynamicClient)
		if err != nil {
			return err
		}
	}

	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	selections := make(chan string)
	go readSelections(signalCtx, stdin, selections)

	_, err = pairFlow(signalCtx, dynamicClient, opts, selections, stdout, nil)
	return err
}

// soleAdapter picks the adapter when a person names
// none. A cluster with one radio needs no name; a cluster with more
// makes the person choose.
func soleAdapter(ctx context.Context, client dynamic.Interface) (string, error) {
	list, err := client.Resource(adapterGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	if len(list.Items) != 1 {
		return "", fmt.Errorf("name an adapter; the cluster has %d", len(list.Items))
	}
	return list.Items[0].GetName(), nil
}

// readSelections turns stdin lines into picks the flow
// reads, and stops when the context ends.
func readSelections(ctx context.Context, stdin io.Reader, selections chan<- string) {
	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		select {
		case selections <- scanner.Text():
		case <-ctx.Done():
			return
		}
	}
}

// pairFlow is the testable core. It opens the window,
// redraws the list on every radio report, approves the first device a
// person picks, and returns the Peripheral name when the pairing
// finishes. A cancelled context deletes the request, which closes the
// window. watching, when it is not nil, is closed once the watch is
// established, so a test can drive events with no race.
func pairFlow(ctx context.Context, client dynamic.Interface, opts pairOptions, selections <-chan string, out io.Writer, watching chan<- struct{}) (string, error) {
	requests := client.Resource(pairingRequestGVR).Namespace(opts.Namespace)

	created, err := requests.Create(ctx, newPairingRequest(opts.Namespace, requestName(), opts.Adapter, opts.Window), metav1.CreateOptions{})
	if err != nil {
		return "", err
	}
	name := created.GetName()

	watcher, err := requests.Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	defer watcher.Stop()
	if watching != nil {
		close(watching)
	}

	devices, _, _ := seenFrom(created)
	fmt.Fprint(out, renderSeen(devices))
	approved := false

	for {
		select {
		case <-ctx.Done():
			closeWindow(client, opts.Namespace, name)
			return "", ctx.Err()
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return "", fmt.Errorf("the pairing window closed early")
			}
			object, ok := event.Object.(*unstructured.Unstructured)
			if !ok || object.GetName() != name {
				continue
			}
			seen, phase, peripheral := seenFrom(object)
			switch phase {
			case phasePaired:
				fmt.Fprintf(out, "paired %s\n", peripheral)
				return peripheral, nil
			case phaseExpired:
				return "", fmt.Errorf("the pairing window expired")
			}
			if !approved {
				devices = seen
				fmt.Fprint(out, renderSeen(devices))
			}
		case line := <-selections:
			if approved {
				continue
			}
			address, err := resolveSelection(line, devices)
			if err != nil {
				fmt.Fprintln(out, err)
				continue
			}
			if err := approve(ctx, requests, name, address); err != nil {
				return "", err
			}
			approved = true
		}
	}
}

// approve writes the chosen address into the request's
// spec.device, the merge patch that stands for a person's approval.
func approve(ctx context.Context, requests dynamic.ResourceInterface, name, address string) error {
	patch, err := approvalPatch(address)
	if err != nil {
		return err
	}
	_, err = requests.Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

// closeWindow deletes the request, which ends the
// radio session. A request already gone is no error, because the goal
// is an absent request.
func closeWindow(client dynamic.Interface, namespace, name string) {
	_ = client.Resource(pairingRequestGVR).Namespace(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
}

// requestName names a window's request. A random
// suffix keeps two windows on one cluster from colliding.
func requestName() string {
	buffer := make([]byte, 4)
	_, _ = rand.Read(buffer)
	return "pair-" + hex.EncodeToString(buffer)
}
