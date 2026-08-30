package main

// These tests run the walk-apply layer against the same
// capturing HTTP server the catalog tests use, so the upserts, the
// reconciliation deletes, and the change signal are proved with no
// Corrosion agent.

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
// so a test asserts what the apply layer wrote without matching whole
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

// oneMovie builds a walk result of a single movie, its file, and its
// aliases, the smallest whole title the apply layer writes.
func oneMovie(id, path string) *walkResult {
	return &walkResult{
		movies:  []movieRow{{Id: id, Library: "house/movies", Kind: libraryKindMovies, Path: path, Title: "T"}},
		files:   []fileRow{{Path: path + "/movie.mkv", Library: "house/movies", Present: true, Items: []string{id}}},
		aliases: []aliasRow{{Alias: id, Item: id, Source: aliasSourceProvider}},
		titles:  1,
	}
}

func TestApplyFullWritesAndReportsChange(t *testing.T) {
	catalog, recorder := recordingCatalog(t)
	state, changed, err := applyFull(context.Background(), catalog, newCatalogState(), oneMovie("movie:tmdb:1", "One"))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("the first walk wrote rows but reported no change")
	}
	kinds := sqlKinds(recorder)
	if !containsKind(kinds, "INSERT MOVIES") || !containsKind(kinds, "INSERT FILES") || !containsKind(kinds, "INSERT ALIASES") {
		t.Errorf("statements = %v, want the item, file, and alias upserts", kinds)
	}
	if !state.movies["movie:tmdb:1"] {
		t.Error("the new state does not hold the movie id")
	}
}

func TestApplyFullDeletesWhatLeftTheVolume(t *testing.T) {
	catalog, recorder := recordingCatalog(t)
	previous, _, err := applyFull(context.Background(), catalog, newCatalogState(), oneMovie("movie:tmdb:1", "One"))
	if err != nil {
		t.Fatal(err)
	}

	// The second walk holds a different title, so the first title and
	// its file left the volume and the apply layer removes their rows.
	state, changed, err := applyFull(context.Background(), catalog, previous, oneMovie("movie:tmdb:2", "Two"))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("a walk that dropped a title reported no change")
	}
	kinds := sqlKinds(recorder)
	if !containsKind(kinds, "DELETE MOVIES") || !containsKind(kinds, "DELETE FILES") || !containsKind(kinds, "DELETE FILE_ITEMS") || !containsKind(kinds, "DELETE ALIASES") {
		t.Errorf("statements = %v, want deletes for the departed title", kinds)
	}
	if state.movies["movie:tmdb:1"] {
		t.Error("the departed movie is still in the state")
	}
}

func TestApplyFullReportsNoChangeOnAnEmptyWalk(t *testing.T) {
	catalog, _ := recordingCatalog(t)
	_, changed, err := applyFull(context.Background(), catalog, newCatalogState(), &walkResult{})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("an empty walk of an empty catalog reported a change")
	}
}

func TestApplyPartialUpsertsWithoutDeleting(t *testing.T) {
	catalog, recorder := recordingCatalog(t)
	previous, _, err := applyFull(context.Background(), catalog, newCatalogState(), oneMovie("movie:tmdb:1", "One"))
	if err != nil {
		t.Fatal(err)
	}

	// A rescan reads one other title, writes it, and leaves the first
	// alone, because it cannot tell what left the rest of the volume.
	state, changed, err := applyPartial(context.Background(), catalog, previous, oneMovie("movie:tmdb:2", "Two"))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("a rescan that wrote a title reported no change")
	}
	if containsKind(sqlKinds(recorder), "DELETE MOVIES") {
		t.Error("a rescan deleted a row")
	}
	if !state.movies["movie:tmdb:1"] || !state.movies["movie:tmdb:2"] {
		t.Errorf("state = %v, want both titles after a merge", state.movies)
	}
}

func TestApplyFullSurfacesAWriteError(t *testing.T) {
	catalog, recorder := recordingCatalog(t)
	recorder.status = 500
	_, _, err := applyFull(context.Background(), catalog, newCatalogState(), oneMovie("movie:tmdb:1", "One"))
	if err == nil {
		t.Error("applyFull hid a catalog write failure")
	}
}

// oneSeries builds a walk result of a series, one episode, and its file.
func oneSeries(seriesID, episodeID, path string) *walkResult {
	return &walkResult{
		series:   []seriesRow{{Id: seriesID, Library: "house/series", Kind: libraryKindSeries, Title: "S"}},
		episodes: []episodeRow{{Id: episodeID, Library: "house/series", Kind: libraryKindSeries, Series: seriesID, Season: 1, Episode: 1}},
		files:    []fileRow{{Path: path, Library: "house/series", Present: true, Items: []string{episodeID}}},
		aliases:  []aliasRow{{Alias: seriesID, Item: seriesID, Source: aliasSourceProvider}},
		titles:   1,
	}
}

func TestApplyFullDeletesADepartedSeriesAndEpisodes(t *testing.T) {
	catalog, recorder := recordingCatalog(t)
	previous, _, err := applyFull(context.Background(), catalog, newCatalogState(),
		oneSeries("series:tvdb:1", "episode:tvdb:1:s01e01", "one.mkv"))
	if err != nil {
		t.Fatal(err)
	}

	// The second walk holds a different series, so the first series, its
	// episode, and its file all left the volume.
	_, changed, err := applyFull(context.Background(), catalog, previous,
		oneSeries("series:tvdb:2", "episode:tvdb:2:s01e01", "two.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("dropping a series reported no change")
	}
	kinds := sqlKinds(recorder)
	if !containsKind(kinds, "DELETE SERIES") || !containsKind(kinds, "DELETE EPISODES") {
		t.Errorf("statements = %v, want the series and episode deletes", kinds)
	}
}
