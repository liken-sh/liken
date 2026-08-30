package main

// These tests run the upsert layer against the same capturing HTTP server
// the catalog tests use, so the item, file, and alias writes and the error
// path are proved with no Corrosion agent. The mark-and-sweep reconciliation
// is proved against a stateful fake in prune_test.go.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// recordingCatalog builds a catalog client and the recorder behind it,
// and ends the server with the test.
func recordingCatalog(t *testing.T) (*Catalog, *catalogRecorder) {
	t.Helper()
	recorder := &catalogRecorder{}
	server := httptest.NewServer(recorder)
	t.Cleanup(server.Close)
	return NewCatalog(server.URL, server.Client()), recorder
}

// sqlKinds reads the leading verb-and-table of every posted statement,
// so a test asserts what the write layer wrote without matching whole
// SQL text.
func sqlKinds(recorder *catalogRecorder) []string {
	var kinds []string
	for _, statement := range recorder.all() {
		fields := strings.Fields(statement.sql)
		if len(fields) >= 3 {
			kinds = append(kinds, strings.ToUpper(fields[0]+" "+fields[2]))
		}
	}
	return kinds
}

func containsKind(kinds []string, want string) bool {
	for _, kind := range kinds {
		if kind == want {
			return true
		}
	}
	return false
}

// postedWith reports whether any posted statement bound the value, so a
// test proves a row was written by the id or path it carries.
func postedWith(recorder *catalogRecorder, value string) bool {
	for _, statement := range recorder.all() {
		for _, param := range statement.params {
			if s, ok := param.(string); ok && s == value {
				return true
			}
		}
	}
	return false
}

// oneMovie builds a walk result of a single movie, its file, and its
// aliases, the smallest whole title the upsert layer writes.
func oneMovie(id, path string) *walkResult {
	return &walkResult{
		movies:  []movieRow{{Id: id, Library: "house/movies", Kind: libraryKindMovies, Path: path, Title: "T"}},
		files:   []fileRow{{Path: path + "/movie.mkv", Library: "house/movies", Present: true, Items: []string{id}}},
		aliases: []aliasRow{{Alias: id, Item: id, Source: aliasSourceProvider}},
		titles:  1,
	}
}

func TestUpsertWalkWritesTheItemFileAndAlias(t *testing.T) {
	catalog, recorder := recordingCatalog(t)
	wrote, err := upsertWalk(context.Background(), catalog, oneMovie("movie:tmdb:1", "One"))
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Error("upsertWalk wrote rows but reported none")
	}
	kinds := sqlKinds(recorder)
	if !containsKind(kinds, "INSERT MOVIES") || !containsKind(kinds, "INSERT FILES") || !containsKind(kinds, "INSERT ALIASES") {
		t.Errorf("statements = %v, want the item, file, and alias upserts", kinds)
	}
}

func TestUpsertWalkReportsNothingWrittenForAnEmptyWalk(t *testing.T) {
	catalog, _ := recordingCatalog(t)
	wrote, err := upsertWalk(context.Background(), catalog, &walkResult{})
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Error("an empty walk reported a write")
	}
}

func TestUpsertWalkSurfacesAWriteError(t *testing.T) {
	catalog, recorder := recordingCatalog(t)
	recorder.status = 500
	_, err := upsertWalk(context.Background(), catalog, oneMovie("movie:tmdb:1", "One"))
	if err == nil {
		t.Error("upsertWalk hid a catalog write failure")
	}
}
