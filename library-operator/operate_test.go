package main

// These tests run the operator against an API server that answers the
// way Kubernetes does, so a pass, the loop around it, and the bus
// handler that feeds it are proved with no cluster and no broker.

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
)

// The operator refuses to start without the settings only the
// Deployment can give it, and it names the one that is missing.
func TestOperateRequiresItsEnvironment(t *testing.T) {
	cases := []struct {
		name    string
		unset   string
		scanner string
		catalog string
		bus     string
	}{
		{name: "no scanner image", unset: scannerImageVariable,
			catalog: testCorrosionImage, bus: testBusAddress},
		{name: "no catalog image", unset: corrosionImageVariable,
			scanner: testScannerImage, bus: testBusAddress},
		{name: "no broker", unset: busAddressVariable,
			scanner: testScannerImage, catalog: testCorrosionImage},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Setenv(scannerImageVariable, one.scanner)
			t.Setenv(corrosionImageVariable, one.catalog)
			t.Setenv(busAddressVariable, one.bus)

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
	t.Setenv(busAddressVariable, testBusAddress)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	err := operate()

	if err == nil || !strings.Contains(err.Error(), "not running in a cluster") {
		t.Fatalf("err = %v, want the in-cluster failure", err)
	}
}

// testClusterEnvironment builds the pod's whole environment: the
// images, the broker, the two address variables, a mounted CA and
// token, and an API server that answers as Kubernetes does. The
// returned channel closes when that server is reached.
//
// The server outlives the test on purpose. The operator's watchers
// have no stop, so the test ends with both held in a watch request,
// and a server that closed would wait on them.
func testClusterEnvironment(t *testing.T, cluster *fakeCluster) chan struct{} {
	t.Helper()
	reached := make(chan struct{})
	var once sync.Once
	handler := cluster.handler()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(reached) })
		handler.ServeHTTP(w, r)
	}))

	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", host)
	t.Setenv("KUBERNETES_SERVICE_PORT", port)
	t.Setenv(scannerImageVariable, testScannerImage)
	t.Setenv(corrosionImageVariable, testCorrosionImage)
	// Port 1 answers nothing, so the bus reconnects for the length of
	// the test and no pass waits on it.
	t.Setenv(busAddressVariable, "127.0.0.1:1")
	t.Setenv(topicBaseVariable, "")

	directory := testServiceAccountDir(t, testCertificatePEM(t, server))
	if err := os.WriteFile(filepath.Join(directory, "token"), []byte("a-service-account-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	serviceAccountDir = directory
	t.Cleanup(func() { serviceAccountDir = defaultServiceAccountDir })
	return reached
}

func TestOperateRunsUntilTheStopSignal(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	reached := testClusterEnvironment(t, cluster)
	returned := make(chan error, 1)
	go func() { returned <- operate() }()

	select {
	case <-reached:
	case err := <-returned:
		t.Fatalf("operate returned %v before it reached the API server", err)
	case <-time.After(10 * time.Second):
		t.Fatal("operate did not reach the API server")
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
	case <-time.After(10 * time.Second):
		t.Fatal("operate did not return after SIGTERM")
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

	if err := library.run(stopped, &reported); err != nil {
		t.Fatal(err)
	}

	line := reported.String()
	if !strings.Contains(line, "1 libraries") || !strings.Contains(line, testBusAddress) {
		t.Errorf("report = %q, want the count and the broker", line)
	}
	if cluster.heldPod("films-scanner") == nil {
		t.Error("the pass created no scanner pod")
	}
}

func TestRunFailsWhenTheCollectionsCannotBeRead(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "the libraries", path: librariesPath},
		{name: "the scanner pods", path: podsAllPath},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			cluster.broken[one.path] = http.StatusInternalServerError
			library := testOperator(t, cluster)

			err := library.run(testRunContext(t), io.Discard)

			if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
				t.Fatalf("err = %v, want the server's own message", err)
			}
		})
	}
}

// A pass reads every Library, and the desk keeps a report only for a
// Library the collection still holds.
func TestPassReconcilesEveryLibraryAndForgetsTheRest(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	library := testOperator(t, cluster)
	library.reports.fold("house", "films", libraryReport{Titles: 12})
	library.reports.fold("house", "gone", libraryReport{Titles: 3})

	library.pass()

	if library.reports.latestFor("house", "films") == nil {
		t.Error("the desk dropped the report of a Library that exists")
	}
	if library.reports.latestFor("house", "gone") != nil {
		t.Error("the desk kept the report of a Library the collection no longer holds")
	}
}

// One library's broken claim does not stop the pass: the other library
// is still reconciled, and the failure is reported and left behind.
func TestPassCarriesOnPastOneBrokenLibrary(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	series := &Library{
		Metadata: ObjectMeta{Name: "series", Namespace: "house", UID: "series-uid"},
		Spec: LibrarySpec{
			Storage: LibraryStorage{Claim: "shows", Root: "/"},
			Kind:    libraryKindSeries,
			Series:  &LibrarySettings{},
		},
	}
	cluster.libraries["series"] = series
	cluster.broken["/api/v1/namespaces/house/persistentvolumeclaims/shows"] = http.StatusInternalServerError

	testOperator(t, cluster).pass()

	if cluster.heldPod("films-scanner") == nil {
		t.Error("the films library was not reconciled")
	}
	if cluster.heldPod("series-scanner") != nil {
		t.Error("a scanner pod was created for a library whose claim could not be read")
	}
}

// A pass that cannot read the collection reports it and reconciles
// nothing, because it does not know what the cluster holds.
func TestPassStopsWhenTheCollectionCannotBeRead(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	cluster.broken[librariesPath] = http.StatusInternalServerError

	testOperator(t, cluster).pass()

	if cluster.countRequests(http.MethodPost, "pods") != 0 {
		t.Error("the pass created a pod without reading the collection")
	}
}

// The bus handler folds a report onto the desk and marks a scanner
// online, and it ignores a message it cannot read rather than failing
// the connection every other library reports over.
func TestHandleBusMessageFoldsWhatItCanRead(t *testing.T) {
	cluster := newFakeCluster()
	library := testOperator(t, cluster)

	library.handleBusMessage(libraryStatusTopic(defaultTopicBase, "house", "films"),
		[]byte(`{"titles":12,"unidentified":2}`))

	report := library.reports.latestFor("house", "films")
	if report == nil {
		t.Fatal("the report did not reach the desk")
	}
	if report.Titles != 12 || report.Unidentified != 2 {
		t.Errorf("report = %+v, want 12 titles and 2 unidentified", report)
	}
}

func TestHandleBusMessageIgnoresWhatItCannotRead(t *testing.T) {
	cases := []struct {
		name    string
		topic   string
		payload string
	}{
		{name: "another operator's topic", topic: "liken/media/plays/house/movie/status", payload: `{"titles":1}`},
		{name: "a topic with no library", topic: defaultTopicBase + "/libraries/house/status", payload: `{"titles":1}`},
		{name: "a report that is not JSON", topic: libraryStatusTopic(defaultTopicBase, "house", "films"), payload: "12 titles"},
		{name: "a cleared report", topic: libraryStatusTopic(defaultTopicBase, "house", "films"), payload: ""},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library := testOperator(t, newFakeCluster())

			library.handleBusMessage(one.topic, []byte(one.payload))

			if library.reports.latestFor("house", "films") != nil {
				t.Error("a message the operator cannot read reached the desk")
			}
		})
	}
}

// A scanner that connects publishes online, and the availability
// carries no report, so the desk holds the flag and nothing else.
func TestHandleBusMessageMarksAScannerOnline(t *testing.T) {
	library := testOperator(t, newFakeCluster())
	woken := make(chan struct{}, 1)
	library.reports = newReports(woken)

	library.handleBusMessage(libraryAvailabilityTopic(defaultTopicBase, "house", "films"),
		[]byte(availabilityOnline))

	select {
	case <-woken:
	case <-time.After(time.Second):
		t.Fatal("the availability woke no pass")
	}
	if library.reports.latestFor("house", "films") != nil {
		t.Error("an availability message folded a report")
	}
}
