package main

// what these tests prove: the departure signal, read against real SQLite
// with the shipped schema.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

// a scanner with only a catalog and a log, which is all the read and the
// report it folds into need.
func scannerOver(catalog *Catalog) *scanner {
	return &scanner{
		statusTopic: libraryStatusTopic(defaultTopicBase, "house", "movies"),
		library:     libraryKey("house", "movies"),
		catalog:     catalog,
		log:         &bytes.Buffer{},
	}
}

// the read names every library with rows in any of the six tables, sorted,
// no repeats; an empty one names none.
func TestLibraryKeysReadsEveryLibraryTheCatalogHolds(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)

	empty, err := catalog.LibraryKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Errorf("keys = %v, want none from an empty catalog", empty)
	}

	seedTwoLibrariesInEveryTable(t, catalog)

	keys, err := catalog.LibraryKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"house/movies", "house/series"}; !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}

// a swept library leaves the set, the whole signal a finalizer is released
// on; the other stays.
func TestLibraryKeysDropsASweptLibrary(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)

	if _, err := catalog.SweepLibrary(t.Context(), "house/movies"); err != nil {
		t.Fatal(err)
	}

	keys, err := catalog.LibraryKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"house/series"}; !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}

// the refresh folds the set in and says whether it moved, so a retained
// report goes out only on a change.
func TestRefreshCatalogLibrariesReportsOnlyAChange(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)
	scan := scannerOver(catalog)

	if !scan.refreshCatalogLibraries(t.Context()) {
		t.Fatal("the first read reported no change")
	}
	if want := []string{"house/movies", "house/series"}; !slices.Equal(scan.report.CatalogLibraries, want) {
		t.Errorf("catalogLibraries = %v, want %v", scan.report.CatalogLibraries, want)
	}

	if scan.refreshCatalogLibraries(t.Context()) {
		t.Error("a second read of an unchanged catalog reported a change")
	}

	if _, err := catalog.SweepLibrary(t.Context(), "house/movies"); err != nil {
		t.Fatal(err)
	}
	if !scan.refreshCatalogLibraries(t.Context()) {
		t.Error("the read after a sweep reported no change")
	}
	if want := []string{"house/series"}; !slices.Equal(scan.report.CatalogLibraries, want) {
		t.Errorf("catalogLibraries = %v, want %v", scan.report.CatalogLibraries, want)
	}
}

// a failed read keeps the last set, so a silent agent never reads as a
// catalog that holds nothing.
func TestRefreshCatalogLibrariesKeepsTheLastSetOnAFailedRead(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)
	scan := scannerOver(catalog)
	scan.refreshCatalogLibraries(t.Context())

	unwell := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(unwell.Close)
	scan.catalog = NewCatalog(unwell.URL, unwell.Client())

	if scan.refreshCatalogLibraries(t.Context()) {
		t.Error("a failed read reported a change")
	}
	if want := []string{"house/movies", "house/series"}; !slices.Equal(scan.report.CatalogLibraries, want) {
		t.Errorf("catalogLibraries = %v, want the last good set %v", scan.report.CatalogLibraries, want)
	}
}

// the watch publishes when the set moves, so a gossiped delete reaches the
// operator in seconds.
func TestWatchCatalogLibrariesPublishesWhenTheSetMoves(t *testing.T) {
	address, accepted := testBroker(t)
	catalog, _ := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)

	intervalWas := catalogLibrariesInterval
	t.Cleanup(func() { catalogLibrariesInterval = intervalWas })
	catalogLibrariesInterval = 5 * time.Millisecond

	scan := scannerOver(catalog)
	scan.bus = newBus(address, "library-house-movies", nil, nil, nil)
	running, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go scan.bus.Run(running)
	broker := <-accepted
	go scan.watchCatalogLibraries(running)

	first := waitForPublish(t, broker.pubs)
	var report libraryReport
	if err := json.Unmarshal(first.payload, &report); err != nil {
		t.Fatal(err)
	}
	if want := []string{"house/movies", "house/series"}; !slices.Equal(report.CatalogLibraries, want) {
		t.Fatalf("the first report names %v, want %v", report.CatalogLibraries, want)
	}

	if _, err := catalog.SweepLibrary(t.Context(), "house/movies"); err != nil {
		t.Fatal(err)
	}

	second := waitForPublish(t, broker.pubs)
	if err := json.Unmarshal(second.payload, &report); err != nil {
		t.Fatal(err)
	}
	if want := []string{"house/series"}; !slices.Equal(report.CatalogLibraries, want) {
		t.Errorf("the report after the sweep names %v, want %v", report.CatalogLibraries, want)
	}
}
