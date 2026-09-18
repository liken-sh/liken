package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// fakeDynamic builds a dynamic client registered with this operator's
// three resource types, so List and Watch return the right list kinds.
func fakeDynamic(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			pairingRequestGVR: "PairingRequestList",
			peripheralGVR:     "PeripheralList",
			adapterGVR:        "AdapterList",
		}, objects...)
}

// syncBuffer is a writer a test goroutine writes and the test reads, so
// the two never race on the rendered output.
type syncBuffer struct {
	mu      sync.Mutex
	builder strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.builder.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.builder.String()
}

func adapterObject(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": driverGroup + "/" + driverVersion,
		"kind":       "Adapter",
		"metadata":   map[string]any{"name": name},
	}}
}

func TestSoleAdapter(t *testing.T) {
	client := fakeDynamic(adapterObject("04-4a-69-66-92-27"))
	name, err := soleAdapter(context.Background(), client)
	if err != nil {
		t.Fatalf("soleAdapter: %v", err)
	}
	if name != "04-4a-69-66-92-27" {
		t.Fatalf("soleAdapter = %q", name)
	}
}

func TestSoleAdapterRefusesWhenNotExactlyOne(t *testing.T) {
	for _, client := range []dynamic.Interface{
		fakeDynamic(),
		fakeDynamic(adapterObject("one"), adapterObject("two")),
	} {
		if _, err := soleAdapter(context.Background(), client); err == nil {
			t.Fatal("soleAdapter named an adapter when the count was not one")
		}
	}
}

// theRequest reads back the one PairingRequest the flow created, so the
// test can drive its status the way the operator would.
func theRequest(t *testing.T, client dynamic.Interface, namespace string) *unstructured.Unstructured {
	t.Helper()
	list, err := client.Resource(pairingRequestGVR).Namespace(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing requests: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("requests = %d, want 1", len(list.Items))
	}
	return &list.Items[0]
}

// setStatus writes a request's status the way the operator does, which
// fires the watch event the flow reads.
func setStatus(t *testing.T, client dynamic.Interface, namespace string, request *unstructured.Unstructured, status map[string]any) {
	t.Helper()
	request = request.DeepCopy()
	if err := unstructured.SetNestedMap(request.Object, status, "status"); err != nil {
		t.Fatalf("setting status: %v", err)
	}
	if _, err := client.Resource(pairingRequestGVR).Namespace(namespace).Update(context.Background(), request, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("updating the request: %v", err)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the condition never held")
}

func TestPairFlowApprovesAPickedDevice(t *testing.T) {
	const namespace = "liken-system"
	client := fakeDynamic()
	selections := make(chan string, 1)
	watching := make(chan struct{})
	out := &syncBuffer{}

	type result struct {
		peripheral string
		err        error
	}
	done := make(chan result, 1)
	go func() {
		peripheral, err := pairFlow(context.Background(), client,
			pairOptions{Namespace: namespace, Adapter: "04-4a-69-66-92-27", Window: 180},
			selections, out, watching)
		done <- result{peripheral, err}
	}()

	<-watching
	request := theRequest(t, client, namespace)

	setStatus(t, client, namespace, request, map[string]any{
		"phase": phaseOpen,
		"seen": []any{
			map[string]any{"address": "A0:AB:51:33:B7:12", "name": "DualSense"},
		},
	})
	waitFor(t, func() bool { return strings.Contains(out.String(), "A0:AB:51:33:B7:12") })

	selections <- "1"
	waitFor(t, func() bool {
		current, err := client.Resource(pairingRequestGVR).Namespace(namespace).Get(context.Background(), request.GetName(), metav1.GetOptions{})
		if err != nil {
			return false
		}
		device, _, _ := unstructured.NestedString(current.Object, "spec", "device")
		return device == "A0:AB:51:33:B7:12"
	})

	setStatus(t, client, namespace, theRequest(t, client, namespace), map[string]any{
		"phase":      phasePaired,
		"peripheral": "a0-ab-51-33-b7-12",
	})

	got := <-done
	if got.err != nil {
		t.Fatalf("pairFlow: %v", got.err)
	}
	if got.peripheral != "a0-ab-51-33-b7-12" {
		t.Fatalf("peripheral = %q", got.peripheral)
	}
}

func TestReadSelections(t *testing.T) {
	selections := make(chan string, 2)
	readSelections(context.Background(), strings.NewReader("1\ntwo\n"), selections)
	if got := <-selections; got != "1" {
		t.Fatalf("first line = %q", got)
	}
	if got := <-selections; got != "two" {
		t.Fatalf("second line = %q", got)
	}
}

func TestRequestName(t *testing.T) {
	name := requestName()
	if !strings.HasPrefix(name, "pair-") {
		t.Fatalf("requestName = %q, want a pair- prefix", name)
	}
	if name == requestName() {
		t.Fatal("two request names collided")
	}
}

func TestPairFlowRejectsABadSelectionAndKeepsGoing(t *testing.T) {
	const namespace = "liken-system"
	client := fakeDynamic()
	selections := make(chan string)
	watching := make(chan struct{})
	out := &syncBuffer{}

	done := make(chan string, 1)
	go func() {
		peripheral, _ := pairFlow(context.Background(), client,
			pairOptions{Namespace: namespace, Adapter: "04-4a-69-66-92-27"},
			selections, out, watching)
		done <- peripheral
	}()

	<-watching
	request := theRequest(t, client, namespace)
	setStatus(t, client, namespace, request, map[string]any{
		"phase": phaseOpen,
		"seen":  []any{map[string]any{"address": "A0:AB:51:33:B7:12", "name": "DualSense"}},
	})
	waitFor(t, func() bool { return strings.Contains(out.String(), "A0:AB:51:33:B7:12") })

	selections <- "9"
	waitFor(t, func() bool { return strings.Contains(out.String(), "no device 9") })

	selections <- "1"
	waitFor(t, func() bool {
		current, err := client.Resource(pairingRequestGVR).Namespace(namespace).Get(context.Background(), request.GetName(), metav1.GetOptions{})
		if err != nil {
			return false
		}
		device, _, _ := unstructured.NestedString(current.Object, "spec", "device")
		return device == "A0:AB:51:33:B7:12"
	})

	setStatus(t, client, namespace, theRequest(t, client, namespace), map[string]any{
		"phase":      phasePaired,
		"peripheral": "a0-ab-51-33-b7-12",
	})
	if got := <-done; got != "a0-ab-51-33-b7-12" {
		t.Fatalf("peripheral = %q", got)
	}
}

func TestPairFlowReportsAnExpiredWindow(t *testing.T) {
	const namespace = "liken-system"
	client := fakeDynamic()
	watching := make(chan struct{})

	done := make(chan error, 1)
	go func() {
		_, err := pairFlow(context.Background(), client,
			pairOptions{Namespace: namespace, Adapter: "04-4a-69-66-92-27"},
			make(chan string), &syncBuffer{}, watching)
		done <- err
	}()

	<-watching
	setStatus(t, client, namespace, theRequest(t, client, namespace), map[string]any{"phase": phaseExpired})

	if err := <-done; err == nil {
		t.Fatal("pairFlow returned no error for an expired window")
	}
}

func TestPairFlowCancelClosesTheWindow(t *testing.T) {
	const namespace = "liken-system"
	client := fakeDynamic()
	watching := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := pairFlow(ctx, client,
			pairOptions{Namespace: namespace, Adapter: "04-4a-69-66-92-27"},
			make(chan string), &syncBuffer{}, watching)
		done <- err
	}()

	<-watching
	cancel()

	if err := <-done; err == nil {
		t.Fatal("pairFlow returned no error after its context was cancelled")
	}
	waitFor(t, func() bool {
		list, err := client.Resource(pairingRequestGVR).Namespace(namespace).List(context.Background(), metav1.ListOptions{})
		return err == nil && len(list.Items) == 0
	})
}
