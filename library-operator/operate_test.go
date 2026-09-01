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
		browser string
		bus     string
	}{
		{name: "no scanner image", unset: scannerImageVariable,
			catalog: testCorrosionImage, browser: testBrowserImage, bus: testBusAddress},
		{name: "no catalog image", unset: corrosionImageVariable,
			scanner: testScannerImage, browser: testBrowserImage, bus: testBusAddress},
		{name: "no media browser image", unset: browserImageVariable,
			scanner: testScannerImage, catalog: testCorrosionImage, bus: testBusAddress},
		{name: "no broker", unset: busAddressVariable,
			scanner: testScannerImage, catalog: testCorrosionImage, browser: testBrowserImage},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Setenv(scannerImageVariable, one.scanner)
			t.Setenv(corrosionImageVariable, one.catalog)
			t.Setenv(browserImageVariable, one.browser)
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
	t.Setenv(browserImageVariable, testBrowserImage)
	t.Setenv(busAddressVariable, testBusAddress)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	err := operate()

	if err == nil || !strings.Contains(err.Error(), "not running in a cluster") {
		t.Fatalf("err = %v, want the in-cluster failure", err)
	}
}

// TestClusterEnvironment builds the pod's whole environment: the
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
	t.Setenv(browserImageVariable, testBrowserImage)
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
	if cluster.heldPod("movies-scanner") == nil {
		t.Error("the pass created no scanner pod")
	}
}

func TestRunFailsWhenTheCollectionsCannotBeRead(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "the libraries", path: librariesPath},
		{name: "the catalogs", path: catalogsPath},
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
	library.reports.fold("house", "movies", libraryReport{Titles: 12})
	library.reports.fold("house", "gone", libraryReport{Titles: 3})

	library.pass()

	if library.reports.latestFor("house", "movies") == nil {
		t.Error("the desk dropped the report of a Library that exists")
	}
	if library.reports.latestFor("house", "gone") != nil {
		t.Error("the desk kept the report of a Library the collection no longer holds")
	}
}

// The desk holds a report only because a retained message stands on
// the bus, so a pass that drops one clears the topics behind it and
// leaves a live library's own topics alone.
func TestPassClearsTheTopicsOfALibraryTheCollectionDoesNotHold(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	operator, broker := operatorOnABroker(t, cluster)
	operator.reports.fold("house", "movies", libraryReport{Titles: 12})
	operator.reports.fold("house", "gone", libraryReport{Titles: 3})
	operator.reports.availability("house", "gone", false)

	operator.pass()

	cleared := clearedTopics(t, broker, 2)
	for _, topic := range libraryTopics("house", "gone") {
		if !cleared[topic] {
			t.Errorf("%s was not cleared", topic)
		}
	}
	for _, topic := range libraryTopics("house", "movies") {
		if cleared[topic] {
			t.Errorf("%s was cleared, and the library still stands", topic)
		}
	}
}

// Litter from a deletion an older release made reaches the desk over
// the subscription at startup, and the first pass clears it.
func TestPassClearsTheLitterOfALibraryItNeverSaw(t *testing.T) {
	cluster := newFakeCluster()
	operator, broker := operatorOnABroker(t, cluster)
	operator.handleBusMessage(libraryStatusTopic(defaultTopicBase, "studio", "films"),
		[]byte(`{"titles":4}`))
	operator.handleBusMessage(libraryAvailabilityTopic(defaultTopicBase, "studio", "films"),
		[]byte(availabilityOffline))

	operator.pass()

	cleared := clearedTopics(t, broker, 2)
	for _, topic := range libraryTopics("studio", "films") {
		if !cleared[topic] {
			t.Errorf("%s was not cleared", topic)
		}
	}
	if operator.reports.latestFor("studio", "films") != nil {
		t.Error("the desk kept the report of a library that never existed")
	}
}

// The operator's own clear comes back on its subscription, and folding
// it would put back the desk state the pass just dropped.
func TestHandleBusMessageIgnoresAClearedAvailability(t *testing.T) {
	operator := testOperator(t, newFakeCluster())

	operator.handleBusMessage(libraryAvailabilityTopic(defaultTopicBase, "house", "movies"), nil)

	if held := operator.reports.retain(map[string]bool{}); len(held) != 0 {
		t.Errorf("the desk holds %v, want nothing from a cleared availability", held)
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

	if cluster.heldPod("movies-scanner") == nil {
		t.Error("the movies library was not reconciled")
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

// A pass stands one catalog Service and one EndpointSlice in every
// namespace that holds a Library, each over that namespace's scanner
// pods and owned by that namespace's Library objects.
func TestPassStandsACatalogInEveryNamespaceThatHoldsALibrary(t *testing.T) {
	cluster := newFakeCluster()
	house := boundHouse(cluster)
	studio := boundStudio(cluster)
	for _, one := range []struct {
		library *Library
		address string
	}{{house, "10.42.1.7"}, {studio, "10.42.3.2"}} {
		pod := standingPod(t, one.library, podRunning, true)
		pod.Spec.NodeName = "nuc-1"
		pod.Status.PodIP = one.address
		cluster.pods[pod.Metadata.Name] = pod
	}

	testOperator(t, cluster).pass()

	for _, one := range []struct {
		namespace string
		owner     string
		address   string
	}{{"house", "house-catalog", "10.42.1.7"}, {"studio", "studio-catalog", "10.42.3.2"}} {
		service := cluster.heldService(one.namespace, catalogServiceName)
		if service == nil {
			t.Fatalf("the pass wrote no catalog Service in %s", one.namespace)
		}
		if len(service.Metadata.OwnerReferences) != 1 ||
			service.Metadata.OwnerReferences[0].Name != one.owner {
			t.Errorf("ownerReferences = %+v, want the Catalog %s",
				service.Metadata.OwnerReferences, one.owner)
		}
		slice := cluster.heldEndpointSlice(one.namespace, catalogServiceName)
		if slice == nil {
			t.Fatalf("the pass wrote no catalog slice in %s", one.namespace)
		}
		if len(slice.Endpoints) != 1 || slice.Endpoints[0].Addresses[0] != one.address {
			t.Errorf("endpoints = %+v, want the scanner pod of %s alone",
				slice.Endpoints, one.namespace)
		}
	}
}

// A pass stands the screen of every delegated Player, in the namespace
// the Player is in, and the screen pod joins that namespace's catalog
// slice beside the scanner pod.
func TestPassStandsTheScreenOfADelegatedPlayer(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	scanner := standingPod(t, library, podRunning, true)
	scanner.Spec.NodeName = "nuc-1"
	scanner.Status.PodIP = "10.42.1.7"
	cluster.pods[scanner.Metadata.Name] = scanner
	seedPlayer(cluster, "den-tv", testLibraryNamespace, screenController)
	seedPlayer(cluster, "kitchen-radio", testLibraryNamespace, "media.liken.sh/idle-screen")
	operator := testOperator(t, cluster)

	operator.pass()

	pod := cluster.heldPod("den-tv-media-browser")
	if pod == nil {
		t.Fatal("the pass stood no screen pod")
	}
	if cluster.heldPod("kitchen-radio-media-browser") != nil {
		t.Error("the pass stood a screen for a Player another controller serves")
	}

	// The pod has no address until the kubelet gives it one, so the
	// slice carries it from the pass after that.
	pod.Status.PodIP = "10.42.2.4"
	pod.Status.InitContainerStatuses = []ContainerStatus{{Name: catalogContainer, Ready: true}}
	pod.Status.ContainerStatuses = []ContainerStatus{{Name: browserContainer, Ready: true}}

	operator.pass()

	slice := cluster.heldEndpointSlice(testLibraryNamespace, catalogServiceName)
	if slice == nil {
		t.Fatal("the pass wrote no catalog slice")
	}
	addresses := []string{}
	for _, endpoint := range slice.Endpoints {
		addresses = append(addresses, endpoint.Addresses[0])
	}
	if len(addresses) != 2 || addresses[0] != "10.42.1.7" || addresses[1] != "10.42.2.4" {
		t.Errorf("addresses = %v, want the scanner and the screen", addresses)
	}
}

// A cluster with no media-operator serves no Players. The pass reports
// that, stands no screen, and reconciles every Library as it would
// otherwise.
func TestPassCarriesOnWithNoPlayersToRead(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	cluster.broken[playersPath] = http.StatusNotFound

	testOperator(t, cluster).pass()

	if cluster.heldPod("movies-scanner") == nil {
		t.Error("the pass stood no scanner pod")
	}
	if cluster.heldLibrary("movies").Status.Conditions == nil {
		t.Error("the pass wrote no status for the library")
	}
}

// A failure to list the pods, to read a Service, or to read a slice is
// reported and does not stop the pass. Every Library still gets its
// status.
func TestPassCarriesOnPastABrokenCatalogObject(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "the pods cannot be listed", path: podsAllPath},
		{name: "the screen pods cannot be listed", path: podsAllPath + "?" + screenPodsQuery},
		{name: "the Service cannot be read",
			path: servicesPath(testLibraryNamespace) + "/" + catalogServiceName},
		{name: "the slice cannot be read",
			path: endpointSlicesPath(testLibraryNamespace) + "/" + catalogServiceName},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			boundHouse(cluster)
			cluster.broken[one.path] = http.StatusInternalServerError

			testOperator(t, cluster).pass()

			if cluster.heldLibrary("movies").Status.Conditions == nil {
				t.Error("the pass wrote no status for the library")
			}
		})
	}
}

// A pass that cannot read the Catalogs reports it and reconciles
// nothing, because the Catalog decides whether a Library proceeds.
func TestPassStopsWhenTheCatalogsCannotBeRead(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	cluster.broken[catalogsPath] = http.StatusInternalServerError

	testOperator(t, cluster).pass()

	if cluster.countRequests(http.MethodPost, "pods") != 0 {
		t.Error("the pass created a pod without reading the Catalogs")
	}
}

// The bus handler folds a report onto the desk and marks a scanner
// online, and it ignores a message it cannot read rather than failing
// the connection every other library reports over.
func TestHandleBusMessageFoldsWhatItCanRead(t *testing.T) {
	cluster := newFakeCluster()
	library := testOperator(t, cluster)

	library.handleBusMessage(libraryStatusTopic(defaultTopicBase, "house", "movies"),
		[]byte(`{"titles":12,"unidentified":2}`))

	report := library.reports.latestFor("house", "movies")
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
		{name: "a report that is not JSON", topic: libraryStatusTopic(defaultTopicBase, "house", "movies"), payload: "12 titles"},
		{name: "a cleared report", topic: libraryStatusTopic(defaultTopicBase, "house", "movies"), payload: ""},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			library := testOperator(t, newFakeCluster())

			library.handleBusMessage(one.topic, []byte(one.payload))

			if library.reports.latestFor("house", "movies") != nil {
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

	library.handleBusMessage(libraryAvailabilityTopic(defaultTopicBase, "house", "movies"),
		[]byte(availabilityOnline))

	select {
	case <-woken:
	case <-time.After(time.Second):
		t.Fatal("the availability woke no pass")
	}
	if library.reports.latestFor("house", "movies") != nil {
		t.Error("an availability message folded a report")
	}
}

// The pass sends a deleting Library to the departure and a standing one to
// the reconcile, in one turn.
func TestPassDepartsADeletingLibraryAndReconcilesTheRest(t *testing.T) {
	cluster := newFakeCluster()
	departingMovies(cluster)
	operator := testOperator(t, cluster)
	seedSurvivor(cluster, operator, true, holding("house/movies"))
	cluster.claims["shows"] = &PersistentVolumeClaim{
		Metadata: ObjectMeta{Name: "shows", Namespace: "house"},
		Spec:     PersistentVolumeClaimSpec{VolumeName: "pv-movies"},
		Status:   PersistentVolumeClaimStatus{Phase: claimBound},
	}

	operator.pass()

	if cluster.heldPod("movies-cleanup") == nil {
		t.Error("the pass stood no cleanup pod for the departing library")
	}
	if cluster.heldPod("movies-scanner") != nil {
		t.Error("the pass stood a scanner for a library on its way out")
	}
	if cluster.heldPod("shows-scanner") == nil {
		t.Error("the surviving library was not reconciled")
	}
	if phase := cluster.heldLibrary("movies").Status.Phase; phase != phaseDeparting {
		t.Errorf("phase = %q, want %s", phase, phaseDeparting)
	}
}

// A cleanup pod's backoff is dropped with the Library it belonged to, so
// the map is as short-lived as the departures.
func TestPassForgetsTheBackoffOfALibraryThatIsGone(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	operator := testOperator(t, cluster)
	operator.cleanupStands["house/movies"] = cleanupStand{count: 1}
	operator.cleanupStands["house/gone"] = cleanupStand{count: 1}

	operator.pass()

	if _, held := operator.cleanupStands["house/movies"]; !held {
		t.Error("the backoff of a Library that exists was dropped")
	}
	if _, held := operator.cleanupStands["house/gone"]; held {
		t.Error("the backoff of a Library the collection no longer holds was kept")
	}
}
