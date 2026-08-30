package main

// These tests read what a pass does with a namespace's Catalog: it
// stands the catalog Service, the EndpointSlice, and the Catalog's own
// status from the one Catalog, and it marks every Catalog Blocked when a
// namespace holds more than one.

import (
	"net/http"
	"testing"
)

// oneNamespace is the byNamespace map a pass builds for one namespace,
// so a test hands reconcileCatalogs the same shape.
func oneNamespace(namespace string, catalogs ...*NamespaceCatalog) map[string][]*NamespaceCatalog {
	return map[string][]*NamespaceCatalog{namespace: catalogs}
}

// A namespace with one Catalog stands the catalog Service and slice
// owned by the Catalog, and the Catalog's status lists the member pods
// and the storage size.
func TestReconcileCatalogsStandsTheClusterFromOneCatalog(t *testing.T) {
	cluster := newFakeCluster()
	catalog := seedCatalog(cluster, "house-catalog", "house")
	pod := scannerPodAt("movies-scanner", "house", "10.42.1.7", "nuc-1")
	cluster.pods["movies-scanner"] = &pod

	testOperator(t, cluster).reconcileCatalogs(oneNamespace("house", catalog))

	service := cluster.heldService("house", catalogServiceName)
	if service == nil || len(service.Metadata.OwnerReferences) != 1 ||
		service.Metadata.OwnerReferences[0].Name != "house-catalog" {
		t.Fatalf("service = %+v, want one owned by the Catalog", service)
	}
	slice := cluster.heldEndpointSlice("house", catalogServiceName)
	if slice == nil || len(slice.Endpoints) != 1 || slice.Endpoints[0].Addresses[0] != "10.42.1.7" {
		t.Fatalf("slice = %+v, want the one scanner pod's address", slice)
	}
	status := cluster.heldCatalog("house-catalog").Status
	if len(status.Members) != 1 || status.Members[0] != "movies-scanner" {
		t.Errorf("members = %v, want the scanner pod", status.Members)
	}
	if status.StorageSize != defaultCatalogSize {
		t.Errorf("storageSize = %q, want the default", status.StorageSize)
	}
	if ready := conditionOf(t, LibraryStatus{Conditions: status.Conditions}, catalogConditionReady); ready.Status != ConditionTrue {
		t.Errorf("Ready = %+v, want True", ready)
	}
}

// A namespace with more than one Catalog stands no cluster, and every
// Catalog in it is marked Blocked with a condition that names the
// conflict.
func TestReconcileCatalogsMarksEveryCatalogBlockedWhenThereAreTwo(t *testing.T) {
	cluster := newFakeCluster()
	first := seedCatalog(cluster, "first", "house")
	second := seedCatalog(cluster, "second", "house")

	testOperator(t, cluster).reconcileCatalogs(oneNamespace("house", first, second))

	if cluster.heldService("house", catalogServiceName) != nil {
		t.Error("the pass stood a catalog Service for a namespace with two Catalogs")
	}
	for _, name := range []string{"first", "second"} {
		status := cluster.heldCatalog(name).Status
		ready := conditionOf(t, LibraryStatus{Conditions: status.Conditions}, catalogConditionReady)
		if ready.Status != ConditionFalse || ready.Reason != catalogReasonManyCatalogs {
			t.Errorf("%s Ready = %+v, want False with ManyCatalogs", name, ready)
		}
		if len(status.Members) != 0 {
			t.Errorf("%s members = %v, want none while blocked", name, status.Members)
		}
	}
}

// A failure to list the scanner pods is reported and stands nothing,
// because the members and the endpoints are read from that list.
func TestReconcileCatalogsStopsWhenThePodsCannotBeListed(t *testing.T) {
	cluster := newFakeCluster()
	catalog := seedCatalog(cluster, "house-catalog", "house")
	cluster.broken[podsAllPath] = http.StatusInternalServerError

	testOperator(t, cluster).reconcileCatalogs(oneNamespace("house", catalog))

	if cluster.heldService("house", catalogServiceName) != nil {
		t.Error("the pass stood a catalog Service without the pods it needs")
	}
}

// The catalog objects' owner is the Catalog, the controller of the one
// cluster its namespace stands.
func TestCatalogObjectOwnerIsTheControllingCatalog(t *testing.T) {
	owner := catalogObjectOwner(&NamespaceCatalog{Metadata: ObjectMeta{Name: "house-catalog", UID: "house-catalog-uid"}})

	if owner.APIVersion != catalogAPIVersion || owner.Kind != "Catalog" {
		t.Errorf("owner = %+v, want the Catalog kind", owner)
	}
	if owner.Name != "house-catalog" || owner.UID != "house-catalog-uid" || !owner.Controller {
		t.Errorf("owner = %+v, want the controlling Catalog", owner)
	}
}

// The standing status lists only the member pods of the Catalog's own
// namespace, in name order, with the storage the agents were given.
func TestStandingCatalogStatusReportsTheNamespacesMembers(t *testing.T) {
	catalog := &NamespaceCatalog{
		Metadata: ObjectMeta{Name: "house-catalog", Namespace: "house"},
		Spec:     CatalogSpec{Storage: CatalogStorage{Size: "4Gi"}},
	}
	pods := []Pod{
		scannerPodAt("shows-scanner", "house", "10.42.1.8", "nuc-1"),
		scannerPodAt("series-scanner", "studio", "10.42.3.2", "nuc-2"),
		scannerPodAt("movies-scanner", "house", "10.42.1.7", "nuc-1"),
	}

	status := standingCatalogStatus(catalog, pods, testNow)

	want := []string{"movies-scanner", "shows-scanner"}
	if len(status.Members) != 2 || status.Members[0] != want[0] || status.Members[1] != want[1] {
		t.Errorf("members = %v, want %v", status.Members, want)
	}
	if status.StorageSize != "4Gi" {
		t.Errorf("storageSize = %q, want the Catalog's size", status.StorageSize)
	}
	ready := conditionOf(t, LibraryStatus{Conditions: status.Conditions}, catalogConditionReady)
	if ready.Status != ConditionTrue || ready.Reason != catalogReasonStanding {
		t.Errorf("Ready = %+v, want True with Standing", ready)
	}
}

// A blocked status reports no members and a condition that names the
// conflict.
func TestBlockedCatalogStatusNamesTheConflict(t *testing.T) {
	catalog := &NamespaceCatalog{Metadata: ObjectMeta{Name: "first", Namespace: "house"}}
	others := []*NamespaceCatalog{catalog, {Metadata: ObjectMeta{Name: "second"}}}

	status := blockedCatalogStatus(catalog, others, testNow)

	if len(status.Members) != 0 {
		t.Errorf("members = %v, want none while blocked", status.Members)
	}
	ready := conditionOf(t, LibraryStatus{Conditions: status.Conditions}, catalogConditionReady)
	if ready.Status != ConditionFalse || ready.Reason != catalogReasonManyCatalogs {
		t.Errorf("Ready = %+v, want False with ManyCatalogs", ready)
	}
	if ready.Message == "" {
		t.Error("the blocked condition carries no message")
	}
}

// The catalog status is written only when it differs, so a settled
// Catalog is quiet and the catalogs watch is not woken by the pass.
func TestWriteCatalogStatusWritesOnlyAChange(t *testing.T) {
	cluster := newFakeCluster()
	seedCatalog(cluster, "house-catalog", "house")
	operator := testOperator(t, cluster)
	catalog := cluster.heldCatalog("house-catalog")
	settled := standingCatalogStatus(catalog, nil, testNow)

	if err := operator.writeCatalogStatus(catalog, settled); err != nil {
		t.Fatal(err)
	}
	if err := operator.writeCatalogStatus(catalog, settled); err != nil {
		t.Fatal(err)
	}
	if got := cluster.countRequests(http.MethodPut, "catalogs"); got != 1 {
		t.Fatalf("status writes = %d, want one", got)
	}

	settled.StorageSize = "9Gi"
	if err := operator.writeCatalogStatus(catalog, settled); err != nil {
		t.Fatal(err)
	}
	if got := cluster.countRequests(http.MethodPut, "catalogs"); got != 2 {
		t.Errorf("status writes = %d, want two", got)
	}
}

// A write another writer got to first is not a failure, and a write the
// server refuses for any other reason is.
func TestWriteCatalogStatusReadsAConflictAsSuccessAndReportsAFailure(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "a conflict is success", status: http.StatusConflict},
		{name: "any other refusal is a failure", status: http.StatusInternalServerError, wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := newFakeCluster()
			seedCatalog(cluster, "house-catalog", "house")
			cluster.broken["/apis/"+libraryAPIVersion+"/namespaces/house/catalogs/house-catalog/status"] = testCase.status
			operator := testOperator(t, cluster)
			catalog := cluster.heldCatalog("house-catalog")

			err := operator.writeCatalogStatus(catalog, standingCatalogStatus(catalog, nil, testNow))

			if testCase.wantErr && err == nil {
				t.Fatal("err = nil, want the server's refusal")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("err = %v, want a conflict to read as success", err)
			}
		})
	}
}
