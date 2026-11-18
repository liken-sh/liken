package main

// The pair verb creates a PairingRequest on one adapter, lists the
// devices that the radio reports as they appear, and writes the address
// of the device the person picks into spec.device, the same write a
// person can make with kubectl. Ctrl-C deletes the request, so the
// radio does not keep scanning with nobody watching.

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
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// pairOptions are the flags of the pair verb, and in Adapter its
// positional argument.
type pairOptions struct {
	Namespace string
	Adapter   string
	Window    int
	Force     bool
}

// runPair checks the operator's version against the CLI's, picks the
// only adapter when the person names none, and runs pairFlow with the
// lines of stdin as selections. Ctrl-C cancels pairFlow's context.
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

// soleAdapter returns the name of the only Adapter in the cluster, and
// fails when the cluster has none or more than one.
func soleAdapter(ctx context.Context, client dynamic.Interface) (string, error) {
	list, err := client.Resource(adapterGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("listing Adapters: %w", err)
	}
	if len(list.Items) != 1 {
		return "", fmt.Errorf("the cluster has %d Adapters, want exactly 1: name one with --adapter", len(list.Items))
	}
	return list.Items[0].GetName(), nil
}

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

// pairFlow creates a PairingRequest, prints the list of seen devices on
// every update to it, and approves the first valid selection. It
// returns the name of the Peripheral when the request reaches Paired,
// and deletes the request when ctx is cancelled. When watching is not
// nil, pairFlow closes it once the watch is open, so a test can update
// the request after that without a race.
func pairFlow(ctx context.Context, client dynamic.Interface, opts pairOptions, selections <-chan string, out io.Writer, watching chan<- struct{}) (string, error) {
	requests := client.Resource(pairingRequestGVR).Namespace(opts.Namespace)

	created, err := requests.Create(ctx, newPairingRequest(opts.Namespace, requestName(), opts.Adapter, opts.Window), metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("creating the PairingRequest: %w", err)
	}
	name := created.GetName()

	// Watch from the create's resourceVersion, so the API server sends
	// only later changes. With no version, the watch would first send
	// the current state of every object it selects.
	watcher, err := requests.Watch(ctx, metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("metadata.name", name).String(),
		ResourceVersion: created.GetResourceVersion(),
	})
	if err != nil {
		return "", fmt.Errorf("watching the PairingRequest %s: %w", name, err)
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
				return "", fmt.Errorf("the watch on the PairingRequest %s closed before the pairing window ended", name)
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
				return "", fmt.Errorf("the pairing window expired with no device paired")
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

// approve writes address to the request's spec.device, which tells the
// operator to pair that device.
func approve(ctx context.Context, requests dynamic.ResourceInterface, name, address string) error {
	patch, err := approvalPatch(address)
	if err != nil {
		return err
	}
	if _, err = requests.Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("setting spec.device of the PairingRequest %s: %w", name, err)
	}
	return nil
}

// closeWindow deletes the request, so the operator stops discovery
// unless another request needs the radio. It ignores
// errors, because the request may already be gone. It uses a new
// context because the caller's context is already cancelled.
func closeWindow(client dynamic.Interface, namespace, name string) {
	_ = client.Resource(pairingRequestGVR).Namespace(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
}

// requestName returns a name with a random suffix, so that two people
// pairing at once do not collide.
func requestName() string {
	buffer := make([]byte, 4)
	_, _ = rand.Read(buffer)
	return "pair-" + hex.EncodeToString(buffer)
}
