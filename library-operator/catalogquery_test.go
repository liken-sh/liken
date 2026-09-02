package main

// These tests prove the query client against a small HTTP server that
// streams the newline-delimited events a Corrosion agent sends, so the row
// decoding, the count decoding, and the error path are proved with no agent.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

// streamingServer answers every query with a fixed body and status, so a
// test drives the stream the client reads.
func streamingServer(t *testing.T, status int, body string) *Catalog {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return NewCatalog(server.URL, server.Client())
}

func TestQueryStringsReadsEveryRow(t *testing.T) {
	body := `{"columns":["id"]}` + "\n" +
		`{"row":[1,["movie:tmdb:1"]]}` + "\n" +
		`{"row":[2,["movie:tmdb:2"]]}` + "\n" +
		`{"eoq":{"time":0.1}}` + "\n"
	catalog := streamingServer(t, http.StatusOK, body)

	ids, err := catalog.queryStrings(context.Background(), "SELECT id FROM movies", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "movie:tmdb:1" || ids[1] != "movie:tmdb:2" {
		t.Errorf("ids = %v, want the two rows", ids)
	}
}

func TestQueryIntReadsACount(t *testing.T) {
	body := `{"columns":["count(*)"]}` + "\n" +
		`{"row":[1,[42]]}` + "\n" +
		`{"eoq":{"time":0.0}}` + "\n"
	catalog := streamingServer(t, http.StatusOK, body)

	count, err := catalog.queryInt(context.Background(), "SELECT count(*) FROM movies", nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 42 {
		t.Errorf("count = %d, want 42", count)
	}
}

func TestQueryReadsNoRowAsZero(t *testing.T) {
	body := `{"columns":["count(*)"]}` + "\n" + `{"eoq":{"time":0.0}}` + "\n"
	catalog := streamingServer(t, http.StatusOK, body)

	count, err := catalog.queryInt(context.Background(), "SELECT count(*) FROM movies", nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0 for an empty result", count)
	}
}

func TestQuerySurfacesAnErrorEvent(t *testing.T) {
	body := `{"columns":["id"]}` + "\n" + `{"error":"no such table: movies"}` + "\n"
	catalog := streamingServer(t, http.StatusOK, body)

	_, err := catalog.queryStrings(context.Background(), "SELECT id FROM movies", nil)
	if err == nil {
		t.Error("the client hid a streamed error event")
	}
}

func TestQueryRejectsAMalformedLine(t *testing.T) {
	catalog := streamingServer(t, http.StatusOK, "this is not json\n")

	_, err := catalog.queryStrings(context.Background(), "SELECT id FROM movies", nil)
	if err == nil {
		t.Error("the client read a malformed line as a row")
	}
}

func TestQueryStringsSendsParameters(t *testing.T) {
	body := `{"columns":["id"]}` + "\n" + `{"row":[1,["one"]]}` + "\n" + `{"eoq":{"time":0.0}}` + "\n"
	catalog := streamingServer(t, http.StatusOK, body)

	ids, err := catalog.queryStrings(context.Background(), "SELECT id FROM movies WHERE library = ?", []any{"house/movies"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "one" {
		t.Errorf("ids = %v, want the one row", ids)
	}
}

func TestQuerySurfacesANon2xxStatus(t *testing.T) {
	catalog := streamingServer(t, http.StatusInternalServerError, "the agent is unwell")

	_, err := catalog.queryStrings(context.Background(), "SELECT id FROM movies", nil)
	if err == nil {
		t.Error("the client hid a non-2xx status")
	}
}

// The read names every library with rows in any replicated table,
// sorted, no repeats; an empty catalog names none.
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

// A library whose only row is its run is a library the reporter
// reports on, so the runs table is one of the tables the read covers.
func TestLibraryKeysNamesALibraryThatOnlyHasARun(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	if err := catalog.UpsertRun(t.Context(), "house/departed",
		libraryRun{Worker: workerCleanup, Job: "cleanup-1", Started: time.Unix(10, 0), Finished: time.Unix(20, 0)}); err != nil {
		t.Fatal(err)
	}

	keys, err := catalog.LibraryKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"house/departed"}; !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}

// A swept library leaves the set, and the other stays.
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

// The count of titles is the movie rows and the series rows, and
// never the episodes under a series.
func TestCountTitlesCountsMoviesAndSeries(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)

	titles, err := catalog.countTitles(t.Context(), "house/movies")
	if err != nil {
		t.Fatal(err)
	}
	if titles != 2 {
		t.Errorf("titles = %d, want the movie and the series", titles)
	}
}
