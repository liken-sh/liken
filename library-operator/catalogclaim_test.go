package main

// These tests read the durable catalog claim the operator provisions:
// its shape, its owner, and that the operator creates it once and
// leaves an existing one alone.

import (
	"net/http"
	"strings"
	"testing"
)

// testCatalogWithSize is a Catalog that names a size and a class, so a
// test reads both onto the claim the operator builds.
func testCatalogWithSize(size, class string) *NamespaceCatalog {
	return &NamespaceCatalog{
		Metadata: ObjectMeta{Name: "house-catalog", Namespace: "house", UID: "house-catalog-uid"},
		Spec:     CatalogSpec{Storage: CatalogStorage{Size: size, StorageClassName: class}},
	}
}

// The catalog claim is ReadWriteOnce, sized from the Catalog, named for
// the Library, and owned by the Library so it survives a pod roll and
// is collected with the Library.
func TestBuildCatalogClaimIsOwnedByItsLibraryAndSizedByTheCatalog(t *testing.T) {
	claim := buildCatalogClaim(studioMovies(), testCatalogWithSize("2Gi", "fast"))

	if claim.Metadata.Name != "movies-catalog" || claim.Metadata.Namespace != "house" {
		t.Errorf("metadata = %+v, want the Library's catalog claim", claim.Metadata)
	}
	if len(claim.Spec.AccessModes) != 1 || claim.Spec.AccessModes[0] != accessModeReadWriteOnce {
		t.Errorf("accessModes = %v, want ReadWriteOnce", claim.Spec.AccessModes)
	}
	if claim.Spec.Resources.Requests["storage"] != "2Gi" {
		t.Errorf("storage = %q, want the Catalog's size", claim.Spec.Resources.Requests["storage"])
	}
	if claim.Spec.StorageClassName != "fast" {
		t.Errorf("storageClassName = %q, want the Catalog's class", claim.Spec.StorageClassName)
	}
	if len(claim.Metadata.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences = %+v, want the Library", claim.Metadata.OwnerReferences)
	}
	owner := claim.Metadata.OwnerReferences[0]
	if owner.Kind != "Library" || owner.Name != "movies" || !owner.Controller {
		t.Errorf("owner = %+v, want the controlling Library", owner)
	}
}

// An empty storageClassName is omitted, so the cluster's default
// StorageClass binds the claim, and the default size fills in when the
// Catalog names none.
func TestBuildCatalogClaimOmitsAnEmptyClassAndDefaultsTheSize(t *testing.T) {
	claim := buildCatalogClaim(studioMovies(), &NamespaceCatalog{})

	if claim.Spec.StorageClassName != "" {
		t.Errorf("storageClassName = %q, want it omitted for the default class", claim.Spec.StorageClassName)
	}
	if claim.Spec.Resources.Requests["storage"] != defaultCatalogSize {
		t.Errorf("storage = %q, want the default", claim.Spec.Resources.Requests["storage"])
	}
}

// The operator creates the catalog claim when there is none.
func TestStandCatalogClaimCreatesTheClaimWhenThereIsNone(t *testing.T) {
	cluster := newFakeCluster()

	if err := testOperator(t, cluster).standCatalogClaim(t.Context(), studioMovies(), testCatalogWithSize("1Gi", "")); err != nil {
		t.Fatal(err)
	}

	claim := cluster.heldClaim("movies-catalog")
	if claim == nil {
		t.Fatal("the operator provisioned no catalog claim")
	}
	if claim.Spec.Resources.Requests["storage"] != "1Gi" {
		t.Errorf("storage = %q, want the Catalog's size", claim.Spec.Resources.Requests["storage"])
	}
}

// A claim that already stands is left alone, so a pod roll starts on
// the catalog it holds rather than a fresh volume.
func TestStandCatalogClaimLeavesAnExistingClaim(t *testing.T) {
	cluster := newFakeCluster()
	cluster.claims["movies-catalog"] = &PersistentVolumeClaim{
		Metadata: ObjectMeta{Name: "movies-catalog", Namespace: "house"},
		Spec:     PersistentVolumeClaimSpec{VolumeName: "pv-catalog"},
		Status:   PersistentVolumeClaimStatus{Phase: claimBound},
	}
	operator := testOperator(t, cluster)

	if err := operator.standCatalogClaim(t.Context(), studioMovies(), testCatalogWithSize("1Gi", "")); err != nil {
		t.Fatal(err)
	}

	if got := cluster.countRequests(http.MethodPost, "persistentvolumeclaims"); got != 0 {
		t.Errorf("creates = %d, want none over a claim that already stands", got)
	}
}

// A create another writer got to first is success. The claim stands,
// and the next pass reads it.
func TestStandCatalogClaimAcceptsAConflict(t *testing.T) {
	cluster := newFakeCluster()
	cluster.refuseCreate = true

	if err := testOperator(t, cluster).standCatalogClaim(t.Context(), studioMovies(), testCatalogWithSize("1Gi", "")); err != nil {
		t.Fatalf("err = %v, want a conflict to read as success", err)
	}
}

// A read that fails for any other reason is a failure the pass reports,
// because it cannot tell whether the claim exists.
func TestStandCatalogClaimReportsAFailedRead(t *testing.T) {
	cluster := newFakeCluster()
	cluster.broken["/api/v1/namespaces/house/persistentvolumeclaims/movies-catalog"] = http.StatusInternalServerError

	err := testOperator(t, cluster).standCatalogClaim(t.Context(), studioMovies(), testCatalogWithSize("1Gi", ""))

	if err == nil || !strings.Contains(err.Error(), "the API server is unwell") {
		t.Fatalf("err = %v, want the server's own message", err)
	}
}

// The claim is created in the Library's own namespace, in the core
// group, and the body carries the ReadWriteOnce access mode.
func TestCreatePersistentVolumeClaimPostsIntoTheNamespace(t *testing.T) {
	client, recorded := recordingAPI(t, PersistentVolumeClaim{
		Metadata: ObjectMeta{Name: "movies-catalog", Namespace: "house"},
	})

	created, err := CreatePersistentVolumeClaim(t.Context(), client, buildCatalogClaim(studioMovies(), testCatalogWithSize("1Gi", "")))
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodPost, "/api/v1/namespaces/house/persistentvolumeclaims")
	if !strings.Contains(recorded.body, `"ReadWriteOnce"`) {
		t.Errorf("body = %s, want the ReadWriteOnce claim", recorded.body)
	}
	if created.Metadata.Name != "movies-catalog" {
		t.Errorf("name = %q, want the claim the server wrote back", created.Metadata.Name)
	}
}
