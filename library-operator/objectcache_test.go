//go:build !pod

package main

// These tests cover the reads of objectcache.go: a pass that reads the
// Catalogs from a watch's store, and a store whose copy is older than
// this operator's own write.

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// storeHolding is a watch's store that holds copies of the objects
// given, as a watch holds them once it has delivered every write. A
// later write to the fake cluster leaves the store older than the API
// server, as a watch that has not delivered the write yet does.
func storeHolding[T any](t *testing.T, objects ...T) storeView {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for index := range objects {
		fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&objects[index])
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Add(&unstructured.Unstructured{Object: fields}); err != nil {
			t.Fatal(err)
		}
	}
	return storeView{store: store, synced: func() bool { return true }}
}

// storeReads answers the Catalogs from a store through the operator's
// memo, the way watch.go does, and every other collection with a list.
type storeReads struct {
	listReads
	catalogs heldObjects
}

func (r storeReads) readCatalogs(ctx context.Context) (*CatalogList, error) {
	items, err := currentList[NamespaceCatalog](ctx, r.client, r.catalogs, keyPath(catalogPath))
	return &CatalogList{Items: items}, err
}

// A Catalog's backfill runs once. The pass that reads the finished Job
// writes Finished on the Catalog, and the TTL then takes the Job. The
// store can still hold the Catalog as it was before that write. The
// operator remembers the version its own write produced, reads the
// Catalog from the API server instead of the store's older copy, and
// does not create the backfill Job a second time.
func TestAPassDoesNotActOnACopyOlderThanItsOwnWrite(t *testing.T) {
	cluster := newFakeCluster()
	catalog := jellyfinCatalog()
	cluster.catalogs[catalog.Metadata.Name] = catalog
	listening := readyProgressPod(catalog)
	if err := stampTemplateHash(&listening.Metadata, listening.Spec); err != nil {
		t.Fatal(err)
	}
	cluster.pods[listening.Metadata.Name] = listening
	cluster.holdJob(backfillJobWith(catalog, JobStatus{Succeeded: 1}))
	operator := testOperator(t, cluster)
	operator.watched = storeReads{
		listReads: listReads{client: operator.client},
		catalogs:  heldObjects{view: storeHolding(t, *catalog), versions: operator.versions.catalogs},
	}
	operator.pass()
	cluster.mutex.Lock()
	delete(cluster.jobs, "house/"+jellyfinBackfillJobName("house"))
	cluster.mutex.Unlock()

	operator.pass()

	if got := cluster.countRequests(http.MethodPost, "jobs"); got != 0 {
		t.Errorf("the passes created %d backfill Jobs, want none after the one that finished", got)
	}
	if status := cluster.heldCatalog("house").Status.Jellyfin; status == nil || status.Backfill != backfillFinished {
		t.Errorf("the Catalog's jellyfin status = %+v, want the finished backfill", status)
	}
}

// A settled pass reads every Catalog from the store, and sends the API
// server no read of one.
func TestASettledPassReadsNoCatalogFromTheAPIServer(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	operator := testOperator(t, cluster)
	// The first pass writes the Catalog's status, and the store then
	// holds that write, as a watch holds it once it delivers it.
	operator.pass()
	operator.watched = storeReads{
		listReads: listReads{client: operator.client},
		catalogs: heldObjects{
			view:     storeHolding(t, *cluster.heldCatalog("house")),
			versions: operator.versions.catalogs,
		},
	}
	before := len(cluster.requestLines())

	operator.pass()

	for _, line := range cluster.requestLines()[before:] {
		if line == http.MethodGet+" "+catalogsPath || line == http.MethodGet+" "+catalogPath("house", "house") {
			t.Errorf("the settled pass sent %s, want the Catalog from the store", line)
		}
	}
}

// A list comes from the store where the store holds a current copy, and
// from the API server where it does not: for a copy older than the
// operator's own write, and for a copy that does not convert. An object
// the API server no longer holds is left out.
func TestAListReadsTheAPIServerWhereTheStoreCannotAnswer(t *testing.T) {
	held := housekeepingCatalog()
	held.Metadata.ResourceVersion = "9"
	unconverted := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "house", "namespace": "house"},
		"spec":     "not an object",
	}}
	for _, c := range []struct {
		name string
		// The copy the store holds.
		stored any
		// The version the memo noted for the Catalog, or none.
		noted string
		// The API server holds the Catalog.
		serving bool
		want    []string
		reads   []string
	}{
		{name: "a current copy", stored: held, noted: "9", serving: true,
			want: []string{"house"}},
		{name: "a copy older than the operator's write", stored: housekeepingCatalog(), noted: "9", serving: true,
			want: []string{"house"}, reads: []string{catalogPath("house", "house")}},
		{name: "a copy that does not convert", stored: unconverted, serving: true,
			want: []string{"house"}, reads: []string{catalogPath("house", "house")}},
		{name: "a copy the API server no longer holds", stored: housekeepingCatalog(), noted: "9",
			reads: []string{catalogPath("house", "house")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cluster := newFakeCluster()
			if c.serving {
				cluster.catalogs["house"] = held
			}
			store := cache.NewStore(cache.MetaNamespaceKeyFunc)
			object, ok := c.stored.(*unstructured.Unstructured)
			if !ok {
				object = storeHolding(t, *c.stored.(*NamespaceCatalog)).store.List()[0].(*unstructured.Unstructured)
			}
			if err := store.Add(object); err != nil {
				t.Fatal(err)
			}
			view := storeView{store: store, synced: func() bool { return true }}
			versions := newVersionMemo()
			if c.noted != "" {
				versions.note("house/house", c.noted)
			}
			client := testOperator(t, cluster).client

			got, err := currentList[NamespaceCatalog](t.Context(), client,
				heldObjects{view: view, versions: versions}, keyPath(catalogPath))

			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, catalog := range got {
				names = append(names, catalog.Metadata.Name)
			}
			if !slices.Equal(names, c.want) {
				t.Errorf("the list = %v, want %v", names, c.want)
			}
			var reads []string
			for _, line := range cluster.requestLines() {
				reads = append(reads, strings.TrimPrefix(line, http.MethodGet+" "))
			}
			if !slices.Equal(reads, c.reads) {
				t.Errorf("the list sent %v, want %v", reads, c.reads)
			}
		})
	}
}

// A provider whose read from the API server fails leaves the list with
// the store's copies, and not with no provider, so no Library reports
// its sources as missing for one failed read.
func TestAProviderThatCannotBeReadKeepsTheStoresCopies(t *testing.T) {
	cluster := newFakeCluster()
	provider := &MetadataProvider{Metadata: ObjectMeta{Name: "tmdb", Namespace: "house"}}
	cluster.providers["tmdb"] = provider
	cluster.broken[metadataProviderPath("house", "tmdb")] = http.StatusInternalServerError
	versions := newVersionMemo()
	versions.note("house/tmdb", "9")

	got := currentOrStored[MetadataProvider](t.Context(), testOperator(t, cluster).client,
		heldObjects{view: storeHolding(t, *provider), versions: versions}, keyPath(metadataProviderPath))

	if len(got) != 1 || got[0].Metadata.Name != "tmdb" {
		t.Errorf("the list = %+v, want the store's copy of tmdb", got)
	}
}
