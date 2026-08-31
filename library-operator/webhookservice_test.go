package main

// These tests read the Service over one scanner pod: the selector that ties
// it to that pod, the ownership that removes it with the Library, the address
// the status reports, and the requests that read and write it.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The Service takes the name of the pod it covers, in the Library's
// namespace, and its selector is the pair of labels that pod carries.
func TestWebhookServiceSelectsItsScannerPod(t *testing.T) {
	library := studioMovies()

	service := buildWebhookService(library)

	if service.Metadata.Name != "movies-scanner" {
		t.Errorf("name = %q, want movies-scanner", service.Metadata.Name)
	}
	if service.Metadata.Namespace != "house" {
		t.Errorf("namespace = %q, want house", service.Metadata.Namespace)
	}
	pod := buildScannerPod(library, testScannerImage, testCorrosionImage, testBusAddress, defaultTopicBase)
	for key, value := range service.Spec.Selector {
		if pod.Metadata.Labels[key] != value {
			t.Errorf("selector %s = %q, want the pod's own %q", key, value, pod.Metadata.Labels[key])
		}
	}
	if len(service.Spec.Selector) != 2 {
		t.Errorf("selector = %v, want the scanner label and the library label", service.Spec.Selector)
	}
}

// The port is the one the scanner listens on. scan.go builds its listen
// address from this same constant, and the container in pod.go declares it
// under the same name.
func TestWebhookServiceCarriesThePortTheScannerListensOn(t *testing.T) {
	service := buildWebhookService(studioMovies())

	want := ServicePort{Name: webhookPortName, Protocol: webhookPortProtocol, Port: webhookPort}
	if len(service.Spec.Ports) != 1 || service.Spec.Ports[0] != want {
		t.Errorf("ports = %+v, want %+v", service.Spec.Ports, want)
	}
}

// The Library owns the Service, so the garbage collector removes it when the
// Library is deleted. This operator has no delete verb for a Service.
func TestWebhookServiceIsOwnedByItsLibrary(t *testing.T) {
	service := buildWebhookService(studioMovies())

	if len(service.Metadata.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences = %+v, want the one Library", service.Metadata.OwnerReferences)
	}
	owner := service.Metadata.OwnerReferences[0]
	if owner.Kind != "Library" || owner.Name != "movies" || owner.UID != "library-uid" || !owner.Controller {
		t.Errorf("owner = %+v, want the controlling Library movies", owner)
	}
}

// The address names the Service and its namespace, and never a pod, so it is
// the same address after every replacement of the pod.
func TestWebhookURLNamesTheServiceAndItsNamespace(t *testing.T) {
	if got := webhookURL(studioMovies()); got != "http://movies-scanner.house.svc:8090/" {
		t.Errorf("webhookURL = %q, want http://movies-scanner.house.svc:8090/", got)
	}
}

func TestStandWebhookServiceCreatesTheServiceWhenThereIsNone(t *testing.T) {
	cluster := newFakeCluster()

	if err := testOperator(t, cluster).standWebhookService(t.Context(), studioMovies()); err != nil {
		t.Fatal(err)
	}

	service := cluster.heldService("house", "movies-scanner")
	if service == nil {
		t.Fatal("the pass wrote no webhook Service")
	}
	if service.Spec.Selector[libraryLabelKey] != "movies" {
		t.Errorf("selector = %v, want the Library's own scanner pod", service.Spec.Selector)
	}
	if len(service.Metadata.OwnerReferences) != 1 || service.Metadata.OwnerReferences[0].Name != "movies" {
		t.Errorf("ownerReferences = %+v, want the one Library", service.Metadata.OwnerReferences)
	}
}

// The operator writes the Service only where the live one differs. An
// unchanged Library costs one GET per pass and no write, and a changed
// selector costs exactly one PUT.
func TestStandWebhookServiceWritesOnDivergenceAlone(t *testing.T) {
	cluster := newFakeCluster()
	operator := testOperator(t, cluster)
	if err := operator.standWebhookService(t.Context(), studioMovies()); err != nil {
		t.Fatal(err)
	}

	if err := operator.standWebhookService(t.Context(), studioMovies()); err != nil {
		t.Fatal(err)
	}

	if writes := cluster.countRequests(http.MethodPut, "services"); writes != 0 {
		t.Errorf("puts = %d, want none over an unchanged Library", writes)
	}

	drifted := cluster.heldService("house", "movies-scanner")
	drifted.Spec.Selector = map[string]string{libraryLabelKey: "shows"}
	cluster.holdService(drifted)

	if err := operator.standWebhookService(t.Context(), studioMovies()); err != nil {
		t.Fatal(err)
	}

	if writes := cluster.countRequests(http.MethodPut, "services"); writes != 1 {
		t.Errorf("puts = %d, want the one write the drifted selector earned", writes)
	}
	if repaired := cluster.heldService("house", "movies-scanner"); repaired.Spec.Selector[libraryLabelKey] != "movies" {
		t.Errorf("selector = %v, want the Library's own scanner pod again", repaired.Spec.Selector)
	}
}

// clusterIP and clusterIPs belong to the API server, and both are immutable,
// so an update writes back the address the read returned.
func TestStandWebhookServiceKeepsTheAddressTheAPIServerAssigned(t *testing.T) {
	cluster := newFakeCluster()
	operator := testOperator(t, cluster)
	if err := operator.standWebhookService(t.Context(), studioMovies()); err != nil {
		t.Fatal(err)
	}
	drifted := cluster.heldService("house", "movies-scanner")
	drifted.Metadata.OwnerReferences = nil
	cluster.holdService(drifted)

	if err := operator.standWebhookService(t.Context(), studioMovies()); err != nil {
		t.Fatal(err)
	}

	service := cluster.heldService("house", "movies-scanner")
	if service.Spec.ClusterIP != assignedClusterIP {
		t.Errorf("clusterIP = %q, want the one the API server assigned", service.Spec.ClusterIP)
	}
	if len(service.Spec.ClusterIPs) != 1 || service.Spec.ClusterIPs[0] != assignedClusterIP {
		t.Errorf("clusterIPs = %v, want the ones the API server assigned", service.Spec.ClusterIPs)
	}
	if service.Metadata.ResourceVersion != "2" {
		t.Errorf("resourceVersion = %q, want the version the update wrote", service.Metadata.ResourceVersion)
	}
}

// A 409 on the create means another writer created the Service first, which
// is the result this pass wanted. The next pass reads that Service and
// compares against it.
func TestStandWebhookServiceTreatsAConflictAsAnotherWriter(t *testing.T) {
	cluster := newFakeCluster()
	cluster.refuseCreate = true

	if err := testOperator(t, cluster).standWebhookService(t.Context(), studioMovies()); err != nil {
		t.Fatal(err)
	}
}

func TestStandWebhookServiceReportsAFailedRead(t *testing.T) {
	cluster := newFakeCluster()
	cluster.broken[servicesPath("house")+"/movies-scanner"] = http.StatusInternalServerError

	err := testOperator(t, cluster).standWebhookService(t.Context(), studioMovies())

	if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
		t.Fatalf("err = %v, want the server's own message", err)
	}
}

func TestStandWebhookServiceReportsAFailedWrite(t *testing.T) {
	cluster := newFakeCluster()
	cluster.broken[servicesPath("house")] = http.StatusInternalServerError

	err := testOperator(t, cluster).standWebhookService(t.Context(), studioMovies())

	if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
		t.Fatalf("err = %v, want the server's own message", err)
	}
}

// A failed update is reported and not swallowed, so the next pass reads the
// Service again and writes again.
func TestStandWebhookServiceReportsAFailedUpdate(t *testing.T) {
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("the API server is unwell"))
			return
		}
		_ = json.NewEncoder(w).Encode(Service{
			Metadata: ObjectMeta{Name: "movies-scanner", Namespace: "house"},
		})
	}))
	operator := newOperator(client, testScannerImage, testCorrosionImage, testBusAddress, defaultTopicBase)

	err := operator.standWebhookService(t.Context(), studioMovies())

	if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
		t.Fatalf("err = %v, want the server's own message", err)
	}
}

// The comparison reads only the fields the operator states. A field the API
// server owns is never a difference to write.
func TestSameWebhookServiceComparesWhatTheOperatorStates(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Service)
		same   bool
	}{
		{name: "the fields the API server filled in", same: true, change: func(live *Service) {
			live.Metadata.ResourceVersion = "9"
			live.Spec.ClusterIP = assignedClusterIP
			live.Spec.ClusterIPs = []string{assignedClusterIP}
		}},
		{name: "the labels", change: func(live *Service) {
			live.Metadata.Labels = nil
		}},
		{name: "the Library that owns it", change: func(live *Service) {
			live.Metadata.OwnerReferences = nil
		}},
		{name: "the not-ready addresses", change: func(live *Service) {
			live.Spec.PublishNotReadyAddresses = true
		}},
		{name: "the selector", change: func(live *Service) {
			live.Spec.Selector[libraryLabelKey] = "shows"
		}},
		{name: "the webhook port", change: func(live *Service) {
			live.Spec.Ports[0].Port = 9999
		}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			live := buildWebhookService(studioMovies())
			one.change(live)

			if got := sameWebhookService(live, buildWebhookService(studioMovies())); got != one.same {
				t.Errorf("sameWebhookService = %v, want %v", got, one.same)
			}
		})
	}
}

// The wire shape is what the API server acts on, so these two fields are
// checked in the marshaled body. The selector is there, so the API server
// keeps the endpoints. publishNotReadyAddresses is false, so a scanner that
// is still starting receives no webhook. The client verbs are the catalog
// Service's own, tested in service_test.go.
func TestWebhookServiceStatesItsSelectorAndTakesOnlyReadyAddresses(t *testing.T) {
	body, err := json.Marshal(buildWebhookService(studioMovies()))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(body), `"selector":{"`+scannerLabelKey+`":"`+scannerLabelValue+`"`) {
		t.Errorf("body = %s, want the selector that names the scanner pod", body)
	}
	if !strings.Contains(string(body), `"publishNotReadyAddresses":false`) {
		t.Errorf("body = %s, want a Service that takes only ready addresses", body)
	}
}
