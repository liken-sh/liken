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

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// storeHolding is a watch's store that holds copies of the objects
// given, as a watch holds them once it has delivered every write. A
// later write to the fake cluster leaves the store older than the API
// server, as a watch that has not delivered the write yet does.
func storeHolding[T any](t *testing.T, objects ...T) informer.View {
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
	return informer.View{Store: store, Synced: func() bool { return true }}
}

// storeReads answers the Catalogs from a store through the operator's
// memo, the way watch.go does, and every other collection with a list.
type storeReads struct {
	listReads
	catalogs informer.Held
}

func (r storeReads) readCatalogs(ctx context.Context) (*CatalogList, error) {
	items, err := informer.CurrentList[NamespaceCatalog](r.client.WithContext(ctx), r.catalogs, memo.NamespacedPath(catalogPath))
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
		catalogs:  informer.Held{View: storeHolding(t, *catalog), Versions: operator.versions.catalogs},
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
		catalogs: informer.Held{
			View:     storeHolding(t, *cluster.heldCatalog("house")),
			Versions: operator.versions.catalogs,
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
				object = storeHolding(t, *c.stored.(*NamespaceCatalog)).Store.List()[0].(*unstructured.Unstructured)
			}
			if err := store.Add(object); err != nil {
				t.Fatal(err)
			}
			view := informer.View{Store: store, Synced: func() bool { return true }}
			versions := memo.New()
			if c.noted != "" {
				versions.Note("house/house", c.noted)
			}
			client := testOperator(t, cluster).client

			got, err := informer.CurrentList[NamespaceCatalog](client.WithContext(t.Context()),
				informer.Held{View: view, Versions: versions}, memo.NamespacedPath(catalogPath))

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
	versions := memo.New()
	versions.Note("house/tmdb", "9")

	got := currentOrCached[MetadataProvider](t.Context(), testOperator(t, cluster).client,
		informer.Held{View: storeHolding(t, *provider), Versions: versions}, memo.NamespacedPath(metadataProviderPath))

	if len(got) != 1 || got[0].Metadata.Name != "tmdb" {
		t.Errorf("the list = %+v, want the store's copy of tmdb", got)
	}
}

// A Service the operator stands is read from the store. Once the store
// holds its first read, a Service it does not hold and the memo has not
// noted does not exist, and the read sends nothing. A Service the memo
// noted, such as one this operator created a moment ago, is read from the
// API server until the store holds it.
func TestAStoodObjectIsReadFromTheAPIServerOnlyWhenTheMemoNotedIt(t *testing.T) {
	service := buildCatalogService("house", nil)
	service.Metadata.ResourceVersion = "3"
	cases := []struct {
		name   string
		stored []Service
		noted  string
		found  bool
		reads  int
	}{
		{name: "a current copy in the store", stored: []Service{*service}, noted: "3", found: true},
		{name: "no copy and no note", found: false},
		{name: "no copy and a note of the create", noted: "3", found: true, reads: 1},
		{name: "an older copy than the operator's write", stored: []Service{*service}, noted: "4", found: true, reads: 1},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			cluster.services["house/catalog"] = service
			versions := memo.New()
			if one.noted != "" {
				versions.Note("house/catalog", one.noted)
			}

			got, err := readStood[Service](t.Context(), testOperator(t, cluster).client,
				informer.Held{View: storeHolding(t, one.stored...), Versions: versions},
				"house/catalog", servicesPath("house")+"/catalog")

			if found := err == nil && got != nil; found != one.found {
				t.Errorf("found = %v (err %v), want %v", found, err, one.found)
			}
			if got := countObjectReads(cluster.requestLines(), "services"); got != one.reads {
				t.Errorf("reads of the Service = %d, want %d", got, one.reads)
			}
		})
	}
}

// jobStoreReads answers the worker Jobs from a store through the
// operator's memo, the way watch.go does, and every other collection
// with a list.
type jobStoreReads struct {
	listReads
	jobs informer.Held
}

func (r jobStoreReads) readWorkerJobs(ctx context.Context) (*JobList, error) {
	return currentJobs(ctx, r.client, r.jobs)
}

// A library Job's name holds the time it was created, so a second create
// never meets a conflict. The store can still lack the walk Job the pass
// created, as a watch does before it delivers the create or while it is
// down. The pass lists the Job it created from the memo, and creates no
// second walk.
func TestAPassDoesNotCreateAJobTwiceBeforeTheWatchDeliversIt(t *testing.T) {
	cluster := newFakeCluster()
	boundHouse(cluster)
	operator := testOperator(t, cluster)
	operator.watched = jobStoreReads{
		listReads: listReads{client: operator.client},
		jobs:      informer.Held{View: storeHolding[Job](t), Versions: operator.versions.jobs},
	}

	operator.pass()
	operator.pass()

	if got := len(cluster.heldJobs()); got != 1 {
		t.Errorf("the passes created %d Jobs, want the one walk", got)
	}
}

// A Job the pass deleted is left out of the next pass's list while the
// store still holds it, and the memo forgets it once the store drops it.
func TestADeletedJobLeavesTheListAndTheMemo(t *testing.T) {
	cluster := newFakeCluster()
	job := Job{Metadata: ObjectMeta{Name: "movies-walk-a", Namespace: "house", ResourceVersion: "3",
		Labels: map[string]string{scannerLabelKey: workerLabelValue}}}
	operator := testOperator(t, cluster)
	if err := operator.deleteJob(t.Context(), "house", "movies-walk-a"); err != nil {
		t.Fatal(err)
	}

	stale, err := currentJobs(t.Context(), operator.client,
		informer.Held{View: storeHolding(t, job), Versions: operator.versions.jobs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := currentJobs(t.Context(), operator.client,
		informer.Held{View: storeHolding[Job](t), Versions: operator.versions.jobs}); err != nil {
		t.Fatal(err)
	}

	if len(stale.Items) != 0 {
		t.Errorf("the list holds %+v, want the deleted Job left out", stale.Items)
	}
	if operator.versions.jobs.Noted("house/movies-walk-a") {
		t.Error("the memo still holds the Job the store dropped")
	}
}
