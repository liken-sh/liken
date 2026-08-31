package main

// These tests read the Catalog wire type and the choice a namespace's
// Catalog objects reduce to: no Catalog, exactly one, or more than one.

import (
	"net/http"
	"strings"
	"testing"
)

// A namespace with no Catalog, one Catalog, and more than one each
// resolve to their own choice, and only exactly one carries a Catalog
// for a Library to proceed against.
func TestSingleCatalogNamesTheChoiceForANamespace(t *testing.T) {
	one := &NamespaceCatalog{Metadata: ObjectMeta{Name: "house-catalog", Namespace: "house"}}
	other := &NamespaceCatalog{Metadata: ObjectMeta{Name: "extra", Namespace: "house"}}
	cases := []struct {
		name       string
		catalogs   []*NamespaceCatalog
		hasCatalog bool
		reason     string
	}{
		{name: "none", catalogs: nil, reason: reasonNoCatalog},
		{name: "exactly one", catalogs: []*NamespaceCatalog{one}, hasCatalog: true},
		{name: "more than one", catalogs: []*NamespaceCatalog{one, other}, reason: reasonManyCatalogs},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			choice := singleCatalog(testCase.catalogs)

			if testCase.hasCatalog {
				if choice.catalog != one {
					t.Errorf("catalog = %+v, want the one Catalog", choice.catalog)
				}
				return
			}
			if choice.catalog != nil {
				t.Errorf("catalog = %+v, want none", choice.catalog)
			}
			if choice.reason != testCase.reason {
				t.Errorf("reason = %q, want %q", choice.reason, testCase.reason)
			}
			if choice.message == "" {
				t.Error("the choice carries no message")
			}
		})
	}
}

// The message a second Catalog earns names the count and the Catalogs,
// in name order, so a person reads which objects conflict.
func TestManyCatalogsMessageNamesTheConflict(t *testing.T) {
	message := manyCatalogsMessage([]*NamespaceCatalog{
		{Metadata: ObjectMeta{Name: "second"}},
		{Metadata: ObjectMeta{Name: "first"}},
	})

	if !strings.Contains(message, "2 Catalogs") {
		t.Errorf("message = %q, want the count", message)
	}
	if !strings.Contains(message, "first, second") {
		t.Errorf("message = %q, want the names in order", message)
	}
}

// The size an agent's volume takes is the Catalog's own value, or the
// small default when it names none.
func TestCatalogStorageSizeFallsBackToTheDefault(t *testing.T) {
	if got := catalogStorageSize(&NamespaceCatalog{Spec: CatalogSpec{Storage: CatalogStorage{Size: "8Gi"}}}); got != "8Gi" {
		t.Errorf("size = %q, want the Catalog's own", got)
	}
	if got := catalogStorageSize(&NamespaceCatalog{}); got != defaultCatalogSize {
		t.Errorf("size = %q, want the default %q", got, defaultCatalogSize)
	}
}

// The Catalogs group by namespace, each list sorted by name, so a pass
// reads the same order every time.
func TestCatalogsByNamespaceGroupAndSort(t *testing.T) {
	byNamespace := catalogsByNamespace([]NamespaceCatalog{
		{Metadata: ObjectMeta{Name: "second", Namespace: "house"}},
		{Metadata: ObjectMeta{Name: "studio-catalog", Namespace: "studio"}},
		{Metadata: ObjectMeta{Name: "first", Namespace: "house"}},
	})

	if len(byNamespace) != 2 {
		t.Fatalf("namespaces = %v, want the two that hold a Catalog", byNamespace)
	}
	house := byNamespace["house"]
	if len(house) != 2 || house[0].Metadata.Name != "first" || house[1].Metadata.Name != "second" {
		t.Errorf("house = %+v, want first then second", house)
	}
	if len(byNamespace["studio"]) != 1 {
		t.Errorf("studio = %+v, want the one Catalog", byNamespace["studio"])
	}
}

// A pass reads every Catalog in the cluster with one request, and the
// list's resourceVersion is where the catalogs watch resumes.
func TestListCatalogsReadsEveryNamespace(t *testing.T) {
	client, recorded := recordingAPI(t, CatalogList{
		Metadata: ListMeta{ResourceVersion: "77"},
		Items:    []NamespaceCatalog{{Metadata: ObjectMeta{Name: "house-catalog", Namespace: "house"}}},
	})

	list, err := ListCatalogs(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodGet, "/apis/library.liken.sh/v1alpha1/catalogs")
	if list.Metadata.ResourceVersion != "77" {
		t.Errorf("resourceVersion = %q, want 77", list.Metadata.ResourceVersion)
	}
	if len(list.Items) != 1 || list.Items[0].Metadata.Name != "house-catalog" {
		t.Errorf("items = %+v, want the one Catalog", list.Items)
	}
}

// The status write goes through the status subresource of the Catalog
// in its own namespace, and it carries the resourceVersion it read.
func TestPutCatalogStatusWritesTheStatusSubresource(t *testing.T) {
	client, recorded := recordingAPI(t, NamespaceCatalog{
		Metadata: ObjectMeta{Name: "house-catalog", Namespace: "house", ResourceVersion: "5"},
	})
	catalog := &NamespaceCatalog{
		Metadata: ObjectMeta{Name: "house-catalog", Namespace: "house", ResourceVersion: "4"},
		Status:   CatalogStatus{StorageSize: "1Gi"},
	}

	written, err := PutCatalogStatus(t.Context(), client, catalog)
	if err != nil {
		t.Fatal(err)
	}

	expectRequest(t, recorded, http.MethodPut,
		"/apis/library.liken.sh/v1alpha1/namespaces/house/catalogs/house-catalog/status")
	if !strings.Contains(recorded.body, `"resourceVersion":"4"`) {
		t.Errorf("body = %s, want the version the read answered", recorded.body)
	}
	if written.Metadata.ResourceVersion != "5" {
		t.Errorf("resourceVersion = %q, want the version the write answered", written.Metadata.ResourceVersion)
	}
}
