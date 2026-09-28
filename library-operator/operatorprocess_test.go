//go:build !pod

package main

// These tests run the whole operator process against the fake cluster:
// its environment, the Lease, the watches, and the loop of passes.

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// The operator refuses to start without the settings only the
// Deployment can give it, and it names the one that is missing.
func TestOperateRequiresItsEnvironment(t *testing.T) {
	cases := []struct {
		name      string
		unset     string
		bus       string
		namespace string
		pod       string
	}{
		{name: "no broker", unset: busAddressVariable,
			namespace: testOperatorNamespace, pod: testOperatorPod},
		{name: "no namespace", unset: operatorNamespaceVariable,
			bus: testBusAddress, pod: testOperatorPod},
		{name: "no pod", unset: podNameVariable,
			bus: testBusAddress, namespace: testOperatorNamespace},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Setenv(busAddressVariable, one.bus)
			t.Setenv(operatorNamespaceVariable, one.namespace)
			t.Setenv(podNameVariable, one.pod)

			err := operate()

			if err == nil || !strings.Contains(err.Error(), one.unset) {
				t.Fatalf("err = %v, want it to name %s", err, one.unset)
			}
		})
	}
}

func TestOperateRefusesOutsideACluster(t *testing.T) {
	t.Setenv(scannerImageVariable, testScannerImage)
	t.Setenv(corrosionImageVariable, testCorrosionImage)
	t.Setenv(browserImageVariable, testBrowserImage)
	t.Setenv(busAddressVariable, testBusAddress)
	t.Setenv(operatorNamespaceVariable, testOperatorNamespace)
	t.Setenv(podNameVariable, testOperatorPod)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	err := operate()

	if err == nil || !strings.Contains(err.Error(), "not running in a cluster") {
		t.Fatalf("err = %v, want the in-cluster failure", err)
	}
}

// TestClusterEnvironment builds the pod's whole environment: the
// images, the broker, the two address variables, a mounted CA and
// token, and an API server that answers as Kubernetes does, with the
// Lease the operator takes. The returned channel closes when that
// server is reached.
func testClusterEnvironment(t *testing.T, cluster *fakeCluster) (chan struct{}, *leaseServer) {
	t.Helper()
	leases := newLeaseServer()
	cluster.leases = leases
	reached := make(chan struct{})
	var once sync.Once
	handler := cluster.handler()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(reached) })
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", host)
	t.Setenv("KUBERNETES_SERVICE_PORT", port)
	// The operator derives its images from its own pod, so the fake
	// cluster serves one and the downward API variable names it.
	cluster.pods[testOperatorPod] = operatorPod(testScannerImage)
	t.Setenv(podNameVariable, testOperatorPod)
	t.Setenv(scannerImageVariable, "")
	t.Setenv(corrosionImageVariable, "")
	t.Setenv(browserImageVariable, "")
	// Port 1 answers nothing, so the bus reconnects for the length of
	// the test and no pass waits on it.
	t.Setenv(busAddressVariable, "127.0.0.1:1")
	t.Setenv(topicBaseVariable, "")
	t.Setenv(operatorNamespaceVariable, testOperatorNamespace)
	// Port zero is a port the kernel picks, so the webhook server of one
	// test never collides with another's.
	t.Setenv(webhookPortVariable, "0")

	directory := testServiceAccountDir(t, testCertificatePEM(t, server))
	if err := os.WriteFile(filepath.Join(directory, "token"), []byte("a-service-account-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	serviceAccountDir = directory
	t.Cleanup(func() { serviceAccountDir = defaultServiceAccountDir })
	return reached, leases
}

// The process runs until SIGTERM, and a SIGTERM leaves the Lease
// released, so a waiting copy takes it at once.
func TestOperateRunsUntilTheStopSignal(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	reached, leases := testClusterEnvironment(t, cluster)
	returned := make(chan error, 1)
	go func() { returned <- operate() }()

	select {
	case <-reached:
	case err := <-returned:
		t.Fatalf("operate returned %v before it reached the API server", err)
	case <-time.After(10 * time.Second):
		t.Fatal("operate did not reach the API server")
	}
	// The first pass runs only once the process holds the Lease and has
	// read the collections, so the walk it starts proves both.
	deadline := time.Now().Add(10 * time.Second)
	for !cluster.heldWalk("house", "movies") {
		if time.Now().After(deadline) {
			t.Fatal("operate ran no pass")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.HasPrefix(leases.holder(), testOperatorPod+"_") {
		t.Errorf("holder = %q while the pass ran, want this pod", leases.holder())
	}

	// The request above happens only after operate registers for the
	// signal, so this signal always reaches operate's handler and never
	// the default one, which would end the test binary.
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("operate did not return after SIGTERM")
	}
	if holder := leases.holder(); holder != "" {
		t.Errorf("holder = %q, want the Lease released", holder)
	}
}

// The loop reconciles before it waits, so one pass runs even when the
// context has already ended, and the line it reports says what it
// operates.
func TestRunReconcilesOnceAndStops(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	library := testOperator(t, cluster)
	stopped, stop := context.WithCancel(context.Background())
	stop()
	var reported strings.Builder

	if err := library.run(stopped, &reported, testWatching(t, library), leading{}); err != nil {
		t.Fatal(err)
	}

	line := reported.String()
	if !strings.Contains(line, "1 libraries") || !strings.Contains(line, testBusAddress) {
		t.Errorf("report = %q, want the count and the broker", line)
	}
	if !cluster.heldWalk("house", "movies") {
		t.Error("the pass started no walk")
	}
}

func TestRunFailsWhenTheCollectionsCannotBeRead(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "the libraries", path: librariesPath},
		{name: "the catalogs", path: catalogsPath},
		{name: "the member pods", path: podsAllPath},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			cluster.broken[one.path] = http.StatusInternalServerError
			library := testOperator(t, cluster)

			err := library.run(testRunContext(t), io.Discard, testWatching(t, library), leading{})

			if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
				t.Fatalf("err = %v, want the server's own message", err)
			}
		})
	}
}

// testWatching is the dynamic client of the watches, pointed at the
// fake cluster the operator's own client reaches.
func testWatching(t *testing.T, o *operator) dynamic.Interface {
	t.Helper()
	client, err := dynamic.NewForConfig(&rest.Config{Host: o.client.base})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// leading is a Lease this process holds from the start, for a test of
// the loop and not of the election. Its step down runs quiet, the way
// the election's does, so the test still stops the watches and the bus.
type leading struct{}

func (leading) await(context.Context) bool { return true }

func (leading) stepDown(quiet func() bool) { quiet() }

// waiting is a Lease another copy holds for the whole test.
type waiting struct{}

func (waiting) await(stop context.Context) bool {
	<-stop.Done()
	return false
}

func (waiting) stepDown(func() bool) {}

// A copy that waits for the Lease serves its webhook, and reads,
// watches, and writes nothing until it leads.
func TestACopyThatWaitsForTheLeaseActsOnNothing(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	library := testOperator(t, cluster)
	library.webhookAddress = "127.0.0.1:0"
	stopped, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stop()

	if err := library.run(stopped, io.Discard, testWatching(t, library), waiting{}); err != nil {
		t.Fatal(err)
	}

	if requests := cluster.countRequests("", ""); requests != 0 || cluster.watching(librariesPath) != 0 {
		t.Errorf("%d requests and %d watches while waiting, want none", requests, cluster.watching(librariesPath))
	}
}
