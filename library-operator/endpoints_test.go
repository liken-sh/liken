package main

// These tests read the catalog EndpointSlice of one namespace: which
// pods reach it, its owners, the order it holds, that a steady
// namespace earns no write, and the three requests the slice is read
// and written with.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// scannerPodAt is a scanner pod as the API server answers one: a
// name, a namespace, a UID, a node, and an address.
func scannerPodAt(name, namespace, address, node string) Pod {
	return Pod{
		Metadata: ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       name + "-uid",
			Labels:    map[string]string{scannerLabelKey: scannerLabelValue},
		},
		Spec:   PodSpec{NodeName: node},
		Status: PodStatus{Phase: podRunning, PodIP: address},
	}
}

// A pod with an address is a peer of its own namespace's cluster. A
// pod in another namespace, a pod with no address, and a pod with a
// deletion timestamp are not.
func TestCatalogEndpointsCarryTheAddressedScannerPodsOfOneNamespace(t *testing.T) {
	pending := scannerPodAt("shows-scanner", testLibraryNamespace, "", "nuc-2")
	leaving := scannerPodAt("music-scanner", testLibraryNamespace, "10.42.2.9", "nuc-3")
	leaving.Metadata.DeletionTimestamp = "2026-08-30T12:00:00Z"

	slice := buildCatalogEndpoints(testLibraryNamespace, nil, []Pod{
		scannerPodAt("movies-scanner", testLibraryNamespace, "10.42.1.7", "nuc-1"),
		scannerPodAt("series-scanner", "studio", "10.42.3.2", "nuc-4"),
		pending,
		leaving,
	})

	if len(slice.Endpoints) != 1 {
		t.Fatalf("endpoints = %+v, want the one addressed pod of the namespace", slice.Endpoints)
	}
	endpoint := slice.Endpoints[0]
	if len(endpoint.Addresses) != 1 || endpoint.Addresses[0] != "10.42.1.7" {
		t.Errorf("addresses = %v, want the pod's own address", endpoint.Addresses)
	}
	if !endpoint.Conditions.Ready {
		t.Error("the endpoint is not ready, so no peer would gossip with it")
	}
	if endpoint.NodeName != "nuc-1" {
		t.Errorf("nodeName = %q, want nuc-1", endpoint.NodeName)
	}
	want := ObjectReference{
		Kind: "Pod", Namespace: testLibraryNamespace,
		Name: "movies-scanner", UID: "movies-scanner-uid",
	}
	if endpoint.TargetRef == nil || *endpoint.TargetRef != want {
		t.Errorf("targetRef = %+v, want %+v", endpoint.TargetRef, want)
	}
}

// The slice carries the service-name label the headless Service is
// found by, and the managed-by label that keeps the slice controllers
// in kube-controller-manager off it.
func TestCatalogEndpointsCarryTheServiceMarks(t *testing.T) {
	slice := buildCatalogEndpoints(testLibraryNamespace, nil, nil)

	if slice.Metadata.Name != catalogServiceName || slice.Metadata.Namespace != testLibraryNamespace {
		t.Errorf("metadata = %+v, want the catalog slice in the Library's namespace", slice.Metadata)
	}
	if slice.Metadata.Labels[serviceNameLabel] != catalogServiceName {
		t.Errorf("labels = %v, want the service-name label", slice.Metadata.Labels)
	}
	if slice.Metadata.Labels[managedByLabel] != endpointSliceManager {
		t.Errorf("labels = %v, want the managed-by label", slice.Metadata.Labels)
	}
	if slice.AddressType != endpointSliceAddressType {
		t.Errorf("addressType = %q, want %q", slice.AddressType, endpointSliceAddressType)
	}
	want := EndpointPort{Name: catalogPortName, Protocol: catalogPortProtocol, Port: catalogPort}
	if len(slice.Ports) != 1 || slice.Ports[0] != want {
		t.Errorf("ports = %+v, want %+v", slice.Ports, want)
	}
}

// The slice is owned by every Library in its namespace, so the
// garbage collector removes it when the last one goes.
func TestCatalogEndpointsAreOwnedByTheLibrariesOfTheirNamespace(t *testing.T) {
	owners := []OwnerReference{catalogOwner("movies", "movies-uid"), catalogOwner("shows", "shows-uid")}

	slice := buildCatalogEndpoints(testLibraryNamespace, owners, nil)

	if len(slice.Metadata.OwnerReferences) != 2 {
		t.Fatalf("ownerReferences = %+v, want both Libraries", slice.Metadata.OwnerReferences)
	}
	if slice.Metadata.OwnerReferences[0] != owners[0] {
		t.Errorf("owner = %+v, want %+v", slice.Metadata.OwnerReferences[0], owners[0])
	}
	if slice.Metadata.OwnerReferences[0].Controller {
		t.Error("an owner is marked the controller, and an object with several owners has none")
	}
}

// With no scanner pods the slice still exists, and it carries an
// empty endpoints list and not null.
func TestCatalogEndpointsWithNoScannerPodsAreEmptyAndNotNull(t *testing.T) {
	body, err := json.Marshal(buildCatalogEndpoints(testLibraryNamespace, nil, nil))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(body), `"endpoints":[]`) {
		t.Errorf("body = %s, want an empty endpoints list", body)
	}
}

// The endpoints sort by address, so two passes over the same pods in
// any order build the same object. That is what makes the
// write-on-divergence comparison work.
func TestCatalogEndpointsSortByAddress(t *testing.T) {
	first := buildCatalogEndpoints(testLibraryNamespace, nil, []Pod{
		scannerPodAt("movies-scanner", testLibraryNamespace, "10.42.1.7", "nuc-1"),
		scannerPodAt("shows-scanner", testLibraryNamespace, "10.42.0.3", "nuc-2"),
	})
	second := buildCatalogEndpoints(testLibraryNamespace, nil, []Pod{
		scannerPodAt("shows-scanner", testLibraryNamespace, "10.42.0.3", "nuc-2"),
		scannerPodAt("movies-scanner", testLibraryNamespace, "10.42.1.7", "nuc-1"),
	})

	if first.Endpoints[0].Addresses[0] != "10.42.0.3" {
		t.Errorf("addresses = %v, want the lowest address first",
			[]string{first.Endpoints[0].Addresses[0], first.Endpoints[1].Addresses[0]})
	}
	firstBody, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBody, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBody) != string(secondBody) {
		t.Errorf("two passes built different slices:\n%s\n%s", firstBody, secondBody)
	}
}

func TestStandCatalogEndpointsCreatesTheSliceWhenThereIsNone(t *testing.T) {
	cluster := newFakeCluster()
	pods := []Pod{scannerPodAt("movies-scanner", testLibraryNamespace, "10.42.1.7", "nuc-1")}

	if err := testOperator(t, cluster).standCatalogEndpoints(testLibraryNamespace, nil, pods); err != nil {
		t.Fatal(err)
	}

	slice := cluster.heldEndpointSlice(testLibraryNamespace, catalogServiceName)
	if slice == nil {
		t.Fatal("the pass wrote no catalog slice")
	}
	if len(slice.Endpoints) != 1 || slice.Endpoints[0].Addresses[0] != "10.42.1.7" {
		t.Errorf("endpoints = %+v, want the scanner pod's address", slice.Endpoints)
	}
}

// The slice is written on divergence only. An unchanged namespace
// costs one GET a pass and no write, and a new pod or a new owner
// earns exactly one PUT.
func TestStandCatalogEndpointsWritesOnDivergenceAlone(t *testing.T) {
	cluster := newFakeCluster()
	operator := testOperator(t, cluster)
	owners := []OwnerReference{catalogOwner("movies", "movies-uid")}
	pods := []Pod{scannerPodAt("movies-scanner", testLibraryNamespace, "10.42.1.7", "nuc-1")}
	if err := operator.standCatalogEndpoints(testLibraryNamespace, owners, pods); err != nil {
		t.Fatal(err)
	}

	if err := operator.standCatalogEndpoints(testLibraryNamespace, owners, pods); err != nil {
		t.Fatal(err)
	}

	if writes := cluster.countRequests(http.MethodPut, "endpointslices"); writes != 0 {
		t.Errorf("puts = %d, want none over an unchanged namespace", writes)
	}

	pods = append(pods, scannerPodAt("shows-scanner", testLibraryNamespace, "10.42.2.4", "nuc-2"))
	if err := operator.standCatalogEndpoints(testLibraryNamespace, owners, pods); err != nil {
		t.Fatal(err)
	}

	if writes := cluster.countRequests(http.MethodPut, "endpointslices"); writes != 1 {
		t.Errorf("puts = %d, want the one write the new pod earned", writes)
	}
	slice := cluster.heldEndpointSlice(testLibraryNamespace, catalogServiceName)
	if len(slice.Endpoints) != 2 {
		t.Errorf("endpoints = %+v, want both scanner pods", slice.Endpoints)
	}

	owners = append(owners, catalogOwner("shows", "shows-uid"))
	if err := operator.standCatalogEndpoints(testLibraryNamespace, owners, pods); err != nil {
		t.Fatal(err)
	}

	if writes := cluster.countRequests(http.MethodPut, "endpointslices"); writes != 2 {
		t.Errorf("puts = %d, want the write the second Library earned", writes)
	}
	if held := cluster.heldEndpointSlice(testLibraryNamespace, catalogServiceName); len(held.Metadata.OwnerReferences) != 2 {
		t.Errorf("ownerReferences = %+v, want both Libraries", held.Metadata.OwnerReferences)
	}
}

// A 409 on the create means another writer got there first, which is
// success. The next pass reads the slice and compares against it.
func TestStandCatalogEndpointsTreatsAConflictAsAnotherWriter(t *testing.T) {
	cluster := newFakeCluster()
	cluster.refuseCreate = true

	if err := testOperator(t, cluster).standCatalogEndpoints(testLibraryNamespace, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestStandCatalogEndpointsReportsAFailedRead(t *testing.T) {
	cluster := newFakeCluster()
	path := endpointSlicesPath(testLibraryNamespace) + "/" + catalogServiceName
	cluster.broken[path] = http.StatusInternalServerError

	err := testOperator(t, cluster).standCatalogEndpoints(testLibraryNamespace, nil, nil)

	if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
		t.Fatalf("err = %v, want the server's own message", err)
	}
}

// The catalog slice is read, created, and updated in the Library's
// own namespace, in the discovery group.
func TestGetEndpointSliceReadsTheCatalogSlice(t *testing.T) {
	client, recorded := recordingAPI(t, EndpointSlice{
		Metadata:    ObjectMeta{Name: "catalog", Namespace: "house", ResourceVersion: "44"},
		AddressType: endpointSliceAddressType,
		Endpoints:   []Endpoint{{Addresses: []string{"10.42.1.7"}}},
	})

	slice, err := GetEndpointSlice(client, "house", "catalog")
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet,
		"/apis/discovery.k8s.io/v1/namespaces/house/endpointslices/catalog")
	if slice.Metadata.ResourceVersion != "44" || slice.Endpoints[0].Addresses[0] != "10.42.1.7" {
		t.Errorf("slice = %+v, want the one the server answered", slice)
	}
}

func TestCreateEndpointSlicePostsIntoTheLibrarysNamespace(t *testing.T) {
	client, recorded := recordingAPI(t, EndpointSlice{
		Metadata: ObjectMeta{Name: "catalog", Namespace: "house"},
	})

	created, err := CreateEndpointSlice(client, buildCatalogEndpoints("house", nil, nil))
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodPost,
		"/apis/discovery.k8s.io/v1/namespaces/house/endpointslices")
	if !strings.Contains(recorded.body, `"kubernetes.io/service-name":"catalog"`) {
		t.Errorf("body = %s, want the slice the operator built", recorded.body)
	}
	if created.Metadata.Name != "catalog" {
		t.Errorf("name = %q, want the slice the server wrote back", created.Metadata.Name)
	}
}

// The write carries the resourceVersion it read, which is what makes
// it conditional.
func TestUpdateEndpointSliceWritesTheVersionItRead(t *testing.T) {
	client, recorded := recordingAPI(t, EndpointSlice{
		Metadata: ObjectMeta{Name: "catalog", Namespace: "house", ResourceVersion: "45"},
	})
	slice := buildCatalogEndpoints("house", nil, nil)
	slice.Metadata.ResourceVersion = "44"

	written, err := UpdateEndpointSlice(client, slice)
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodPut,
		"/apis/discovery.k8s.io/v1/namespaces/house/endpointslices/catalog")
	if !strings.Contains(recorded.body, `"resourceVersion":"44"`) {
		t.Errorf("body = %s, want the version the read answered", recorded.body)
	}
	if written.Metadata.ResourceVersion != "45" {
		t.Errorf("resourceVersion = %q, want the version the write answered",
			written.Metadata.ResourceVersion)
	}
}

// The comparison reads only what the operator states: the owners, the
// endpoints, and the ports.
func TestSameEndpointsComparesWhatTheOperatorStates(t *testing.T) {
	owners := []OwnerReference{catalogOwner("movies", "movies-uid")}
	pods := []Pod{scannerPodAt("movies-scanner", testLibraryNamespace, "10.42.1.7", "nuc-1")}
	cases := []struct {
		name   string
		change func(*EndpointSlice)
		same   bool
	}{
		{name: "the fields the API server filled in", same: true, change: func(live *EndpointSlice) {
			live.Metadata.ResourceVersion = "9"
		}},
		{name: "a Library that owns it", change: func(live *EndpointSlice) {
			live.Metadata.OwnerReferences = nil
		}},
		{name: "how many pods are peers", change: func(live *EndpointSlice) {
			live.Endpoints = nil
		}},
		{name: "the address one pod holds", change: func(live *EndpointSlice) {
			live.Endpoints[0].Addresses = []string{"10.42.1.8"}
		}},
		{name: "the gossip port", change: func(live *EndpointSlice) {
			live.Ports[0].Port = 9999
		}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			live := buildCatalogEndpoints(testLibraryNamespace, owners, pods)
			one.change(live)

			desired := buildCatalogEndpoints(testLibraryNamespace, owners, pods)
			if got := sameEndpoints(live, desired); got != one.same {
				t.Errorf("sameEndpoints = %v, want %v", got, one.same)
			}
		})
	}
}
