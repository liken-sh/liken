//go:build !pod

package main

// These tests run the whole operator process against the fake cluster:
// its environment, the Lease, the watches, and the loop of passes.

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/election"
	"k8s.io/client-go/dynamic"
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

// The watches and the Lease reach the API server at the address the
// environment names, with the CA and the token file of the
// ServiceAccount, and send JSON. The token is named by its file, so
// client-go reads it again after the kubelet rotates it.
func TestTheInClusterConfigReadsTheServiceAccount(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")

	config := inClusterConfig()

	if config.Host != "https://10.43.0.1:443" || config.ContentType != "application/json" ||
		config.BearerTokenFile != serviceAccountDir+"/token" || config.CAFile != serviceAccountDir+"/ca.crt" {
		t.Errorf("config = %s %s %s %s, want the Service's address, JSON, and the ServiceAccount's files",
			config.Host, config.ContentType, config.BearerTokenFile, config.CAFile)
	}
}

// The process does not start without a pod to name in the Lease, or
// without its own pod to read the images from.
func TestStartProcessRefusesWhatItCannotName(t *testing.T) {
	cases := []struct {
		name string
		pod  string
		want string
	}{
		{name: "no pod for the Lease", pod: "", want: "leader election"},
		{name: "a pod the cluster does not hold", pod: "library-operator-gone", want: "library-operator-gone"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Setenv(podNameVariable, one.pod)
			synctest.Test(t, func(t *testing.T) {
				server := newFakeCluster().start(t)
				settings := processSettings{busAddress: testBusAddress, namespace: testOperatorNamespace,
					pod: one.pod, topicBase: defaultTopicBase, port: "8080"}

				_, _, _, err := startProcess(t.Context(), settings,
					apiclient.New(apiservertest.Host, server.Client(), ""), server.Config(),
					func(int) {}, io.Discard)

				if err == nil || !strings.Contains(err.Error(), one.want) {
					t.Errorf("err = %v, want it to name %q", err, one.want)
				}
			})
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

// The process runs until the stop, and the stop leaves the Lease
// released, so a waiting copy takes it at once. Its writes go through
// the write guard: the walk Job of the first pass lands while the
// process leads, and the same client refuses a write once it has
// stepped down.
func TestTheOperatorProcessRunsUntilTheStop(t *testing.T) {
	t.Setenv(scannerImageVariable, "")
	t.Setenv(corrosionImageVariable, "")
	t.Setenv(browserImageVariable, "")
	// The operator derives its images from its own pod, which the
	// downward API variable names.
	t.Setenv(podNameVariable, testOperatorPod)
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		boundHouse(cluster)
		cluster.pods[testOperatorPod] = operatorPod(testScannerImage)
		leases := newLeaseServer()
		cluster.leases = leases
		server := cluster.start(t)
		config := server.Config()
		config.ContentType = "application/json"
		settings := processSettings{busAddress: testBusAddress, namespace: testOperatorNamespace,
			pod: testOperatorPod, topicBase: defaultTopicBase, port: "8080"}
		stopped, stop := context.WithCancel(t.Context())
		var log strings.Builder

		library, leader, watching, err := startProcess(stopped, settings,
			apiclient.New(apiservertest.Host, server.Client(), ""), config,
			func(code int) { t.Errorf("the process exited with %d", code) }, &log)
		if err != nil {
			t.Fatal(err)
		}
		library.listen = func(string, string) (net.Listener, error) { return newPipeListener(), nil }
		library.bus.dial = func(context.Context) (net.Conn, error) { return nil, errors.New("no broker") }
		returned := make(chan error, 1)
		go func() { returned <- library.run(stopped, &log, watching, leader) }()
		synctest.Wait()

		if !cluster.heldWalk("house", "movies") {
			t.Error("the process ran no pass")
		}
		if !strings.HasPrefix(leases.Holder(), testOperatorPod+"_") {
			t.Errorf("holder = %q while the pass ran, want this pod", leases.Holder())
		}

		stop()
		synctest.Wait()

		select {
		case err := <-returned:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("the process did not return after the stop")
		}
		if holder := leases.Holder(); holder != "" {
			t.Errorf("holder = %q, want the Lease released", holder)
		}
		err = library.client.Request(http.MethodPost, podsPath(testOperatorNamespace), jsonContentType, []byte("{}"), nil)
		if !errors.Is(err, election.ErrNotLeading) {
			t.Errorf("a write after the step down answered %v, want %v", err, election.ErrNotLeading)
		}
	})
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

	if err := library.run(stopped, &reported, testWatching(t, cluster), leading{}); err != nil {
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

			err := library.run(testRunContext(t), io.Discard, testWatching(t, cluster), leading{})

			if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
				t.Fatalf("err = %v, want the server's own message", err)
			}
		})
	}
}

// testWatching is the dynamic client of the watches, pointed at the
// fake cluster the operator's own client reaches.
func testWatching(t *testing.T, cluster *fakeCluster) dynamic.Interface {
	t.Helper()
	client, err := dynamic.NewForConfig(cluster.start(t).Config())
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
// watches, and writes nothing until it leads. It holds the path the
// webhook names for the pass it runs once it leads.
func TestACopyThatWaitsForTheLeaseActsOnNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		boundHouse(cluster)
		library := testOperator(t, cluster)
		listener := newPipeListener()
		library.listen = func(string, string) (net.Listener, error) { return listener, nil }
		stopped, stop := context.WithCancel(t.Context())
		returned := make(chan error, 1)
		go func() { returned <- library.run(stopped, io.Discard, testWatching(t, cluster), waiting{}) }()
		synctest.Wait()

		answer, err := listener.Client().Post("http://library-operator/webhook/house/movies",
			"application/json", strings.NewReader(`{"movie":{"folderPath":"/srv/media/Some Film (1999)"}}`))
		if err != nil {
			t.Fatal(err)
		}
		drain(answer.Body)
		time.Sleep(time.Hour)
		stop()
		synctest.Wait()

		if answer.StatusCode != http.StatusNoContent || len(library.paths.held("house", "movies")) != 1 {
			t.Errorf("status = %d and held = %q, want the webhook answered and its path held",
				answer.StatusCode, library.paths.held("house", "movies"))
		}
		if requests := cluster.countRequests("", ""); requests != 0 || cluster.watching(librariesPath) != 0 {
			t.Errorf("%d requests and %d watches while waiting, want none", requests, cluster.watching(librariesPath))
		}
		if err := <-returned; err != nil {
			t.Fatal(err)
		}
	})
}
