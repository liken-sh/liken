package main

// These tests read the catalog Service of one namespace: that it is
// headless, that its owners are the namespace's Library objects, that
// a steady namespace earns no write, that an update keeps the address
// the API server assigned, and the three requests the Service is read
// and written with.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The Service is headless, so the name resolves to every peer's own
// address, and it publishes not-ready addresses, so an agent that is
// starting is still a peer.
func TestCatalogServiceIsHeadlessAndPublishesEveryAddress(t *testing.T) {
	service := buildCatalogService(testLibraryNamespace, nil)

	if service.Metadata.Name != catalogServiceName || service.Metadata.Namespace != testLibraryNamespace {
		t.Errorf("metadata = %+v, want the catalog Service in the Library's namespace", service.Metadata)
	}
	if service.Spec.ClusterIP != headlessClusterIP {
		t.Errorf("clusterIP = %q, want %q", service.Spec.ClusterIP, headlessClusterIP)
	}
	if !service.Spec.PublishNotReadyAddresses {
		t.Error("the Service drops not-ready addresses, so a forming cluster has no peers")
	}
	want := ServicePort{Name: catalogPortName, Protocol: catalogPortProtocol, Port: catalogPort}
	if len(service.Spec.Ports) != 1 || service.Spec.Ports[0] != want {
		t.Errorf("ports = %+v, want %+v", service.Spec.Ports, want)
	}
}

// The catalog Service states no selector. A selector would make the API
// server write the endpoints, in place of the EndpointSlice this operator
// writes, so the field must be absent from the body and not merely empty.
func TestCatalogServiceNamesNoSelector(t *testing.T) {
	body, err := json.Marshal(buildCatalogService(testLibraryNamespace, nil))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(body), "selector") {
		t.Errorf("body = %s, want no selector at all", body)
	}
}

// The Service is owned by its namespace's one Catalog, so the garbage
// collector removes it when the Catalog goes.
func TestCatalogServiceIsOwnedByItsCatalog(t *testing.T) {
	owners := []OwnerReference{catalogOwner("house-catalog", "house-catalog-uid")}

	service := buildCatalogService(testLibraryNamespace, owners)

	if len(service.Metadata.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences = %+v, want the one Catalog", service.Metadata.OwnerReferences)
	}
	if service.Metadata.OwnerReferences[0] != owners[0] {
		t.Errorf("owner = %+v, want %+v", service.Metadata.OwnerReferences[0], owners[0])
	}
	if service.Metadata.OwnerReferences[0].Kind != "Catalog" || !service.Metadata.OwnerReferences[0].Controller {
		t.Errorf("owner = %+v, want the controlling Catalog", service.Metadata.OwnerReferences[0])
	}
}

func TestStandCatalogServiceCreatesTheServiceWhenThereIsNone(t *testing.T) {
	cluster := newFakeCluster()
	owners := []OwnerReference{catalogOwner("movies", "movies-uid")}

	if err := testOperator(t, cluster).standCatalogService(testLibraryNamespace, owners); err != nil {
		t.Fatal(err)
	}

	service := cluster.heldService(testLibraryNamespace, catalogServiceName)
	if service == nil {
		t.Fatal("the pass wrote no catalog Service")
	}
	if service.Spec.ClusterIP != headlessClusterIP || !service.Spec.PublishNotReadyAddresses {
		t.Errorf("spec = %+v, want a headless Service that publishes every address", service.Spec)
	}
	if len(service.Metadata.OwnerReferences) != 1 || service.Metadata.OwnerReferences[0].Name != "movies" {
		t.Errorf("ownerReferences = %+v, want the one Library", service.Metadata.OwnerReferences)
	}
}

// The Service is written on divergence only. An unchanged namespace
// costs one GET a pass and no write, and a new owner earns exactly one
// PUT.
func TestStandCatalogServiceWritesOnDivergenceAlone(t *testing.T) {
	cluster := newFakeCluster()
	operator := testOperator(t, cluster)
	owners := []OwnerReference{catalogOwner("movies", "movies-uid")}
	if err := operator.standCatalogService(testLibraryNamespace, owners); err != nil {
		t.Fatal(err)
	}

	if err := operator.standCatalogService(testLibraryNamespace, owners); err != nil {
		t.Fatal(err)
	}

	if writes := cluster.countRequests(http.MethodPut, "services"); writes != 0 {
		t.Errorf("puts = %d, want none over an unchanged namespace", writes)
	}

	owners = append(owners, catalogOwner("shows", "shows-uid"))
	if err := operator.standCatalogService(testLibraryNamespace, owners); err != nil {
		t.Fatal(err)
	}

	if writes := cluster.countRequests(http.MethodPut, "services"); writes != 1 {
		t.Errorf("puts = %d, want the one write the second Library earned", writes)
	}
	service := cluster.heldService(testLibraryNamespace, catalogServiceName)
	if len(service.Metadata.OwnerReferences) != 2 {
		t.Errorf("ownerReferences = %+v, want both Libraries", service.Metadata.OwnerReferences)
	}
}

// clusterIP and clusterIPs belong to the API server and both are
// immutable, so an update carries back what the read answered.
func TestStandCatalogServiceKeepsTheAddressTheAPIServerAssigned(t *testing.T) {
	cluster := newFakeCluster()
	operator := testOperator(t, cluster)
	if err := operator.standCatalogService(testLibraryNamespace, nil); err != nil {
		t.Fatal(err)
	}

	if err := operator.standCatalogService(testLibraryNamespace,
		[]OwnerReference{catalogOwner("movies", "movies-uid")}); err != nil {
		t.Fatal(err)
	}

	service := cluster.heldService(testLibraryNamespace, catalogServiceName)
	if service.Spec.ClusterIP != headlessClusterIP {
		t.Errorf("clusterIP = %q, want the one the API server assigned", service.Spec.ClusterIP)
	}
	if len(service.Spec.ClusterIPs) != 1 || service.Spec.ClusterIPs[0] != headlessClusterIP {
		t.Errorf("clusterIPs = %v, want the ones the API server assigned", service.Spec.ClusterIPs)
	}
	if service.Metadata.ResourceVersion != "2" {
		t.Errorf("resourceVersion = %q, want the version the update wrote", service.Metadata.ResourceVersion)
	}
}

// A 409 on the create means another writer got there first, which is
// success. The next pass reads the Service and compares against it.
// A selector on the catalog Service would make the API server write the
// endpoints, in place of the EndpointSlice this operator writes, and the
// agents would lose their peer list. A pass clears one that reaches the
// object by another hand.
func TestStandCatalogServiceClearsASelectorItDidNotWrite(t *testing.T) {
	cluster := newFakeCluster()
	operator := testOperator(t, cluster)
	owners := []OwnerReference{catalogOwner("movies", "movies-uid")}
	if err := operator.standCatalogService(testLibraryNamespace, owners); err != nil {
		t.Fatal(err)
	}

	live := cluster.heldService(testLibraryNamespace, catalogServiceName)
	live.Spec.Selector = map[string]string{"app": "catalog"}
	cluster.holdService(live)

	if err := operator.standCatalogService(testLibraryNamespace, owners); err != nil {
		t.Fatal(err)
	}

	if selector := cluster.heldService(testLibraryNamespace, catalogServiceName).Spec.Selector; len(selector) != 0 {
		t.Errorf("selector = %v, want it cleared", selector)
	}
}

func TestStandCatalogServiceTreatsAConflictAsAnotherWriter(t *testing.T) {
	cluster := newFakeCluster()
	cluster.refuseCreate = true

	if err := testOperator(t, cluster).standCatalogService(testLibraryNamespace, nil); err != nil {
		t.Fatal(err)
	}
}

func TestStandCatalogServiceReportsAFailedRead(t *testing.T) {
	cluster := newFakeCluster()
	path := servicesPath(testLibraryNamespace) + "/" + catalogServiceName
	cluster.broken[path] = http.StatusInternalServerError

	err := testOperator(t, cluster).standCatalogService(testLibraryNamespace, nil)

	if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
		t.Fatalf("err = %v, want the server's own message", err)
	}
}

// The catalog Service is read, created, and updated in the Library's
// own namespace, in the core group.
func TestGetServiceReadsTheCatalogService(t *testing.T) {
	client, recorded := recordingAPI(t, Service{
		Metadata: ObjectMeta{Name: "catalog", Namespace: "house", ResourceVersion: "44"},
		Spec:     ServiceSpec{ClusterIP: headlessClusterIP, ClusterIPs: []string{headlessClusterIP}},
	})

	service, err := GetService(client, "house", "catalog")
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/api/v1/namespaces/house/services/catalog")
	if service.Metadata.ResourceVersion != "44" || service.Spec.ClusterIP != headlessClusterIP {
		t.Errorf("service = %+v, want the one the server answered", service)
	}
}

func TestCreateServicePostsIntoTheLibrarysNamespace(t *testing.T) {
	client, recorded := recordingAPI(t, Service{
		Metadata: ObjectMeta{Name: "catalog", Namespace: "house"},
	})

	created, err := CreateService(client, buildCatalogService("house", nil))
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodPost, "/api/v1/namespaces/house/services")
	if !strings.Contains(recorded.body, `"clusterIP":"None"`) {
		t.Errorf("body = %s, want the headless Service the operator built", recorded.body)
	}
	if !strings.Contains(recorded.body, `"publishNotReadyAddresses":true`) {
		t.Errorf("body = %s, want the not-ready addresses a forming cluster needs", recorded.body)
	}
	if created.Metadata.Name != "catalog" {
		t.Errorf("name = %q, want the Service the server wrote back", created.Metadata.Name)
	}
}

// The write carries the resourceVersion it read, which is what makes
// it conditional.
func TestUpdateServiceWritesTheVersionItRead(t *testing.T) {
	client, recorded := recordingAPI(t, Service{
		Metadata: ObjectMeta{Name: "catalog", Namespace: "house", ResourceVersion: "45"},
	})
	service := buildCatalogService("house", nil)
	service.Metadata.ResourceVersion = "44"

	written, err := UpdateService(client, service)
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodPut, "/api/v1/namespaces/house/services/catalog")
	if !strings.Contains(recorded.body, `"resourceVersion":"44"`) {
		t.Errorf("body = %s, want the version the read answered", recorded.body)
	}
	if written.Metadata.ResourceVersion != "45" {
		t.Errorf("resourceVersion = %q, want the version the write answered",
			written.Metadata.ResourceVersion)
	}
}

// The comparison reads only what the operator states. A field the API
// server owns never counts as a divergence.
func TestSameServiceComparesWhatTheOperatorStates(t *testing.T) {
	owners := []OwnerReference{catalogOwner("movies", "movies-uid")}
	cases := []struct {
		name   string
		change func(*Service)
		same   bool
	}{
		{name: "the fields the API server filled in", same: true, change: func(live *Service) {
			live.Metadata.ResourceVersion = "9"
			live.Spec.ClusterIPs = []string{headlessClusterIP}
		}},
		{name: "a Library that owns it", change: func(live *Service) {
			live.Metadata.OwnerReferences = nil
		}},
		{name: "the headless address", change: func(live *Service) {
			live.Spec.ClusterIP = "10.43.0.9"
		}},
		{name: "the not-ready addresses", change: func(live *Service) {
			live.Spec.PublishNotReadyAddresses = false
		}},
		{name: "the gossip port", change: func(live *Service) {
			live.Spec.Ports[0].Port = 9999
		}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			live := buildCatalogService(testLibraryNamespace, owners)
			one.change(live)

			if got := sameService(live, buildCatalogService(testLibraryNamespace, owners)); got != one.same {
				t.Errorf("sameService = %v, want %v", got, one.same)
			}
		})
	}
}
