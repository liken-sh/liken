package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// An agent that takes every write and refuses every read, so a test drives a
// container whose gap read fails after its first write landed.
func writeOnlyCatalog(t *testing.T) *Catalog {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, queriesPath) {
			http.Error(w, "database is locked", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"rows_affected": 1}}})
	}))
	t.Cleanup(server.Close)
	return NewCatalog(server.URL, server.Client())
}

// An agent that answers the gap read once and refuses every read after it, so
// a test drives a container whose per-item read fails mid-run.
func oneGapThenRefuses(t *testing.T, id string) *Catalog {
	t.Helper()
	var mutex sync.Mutex
	served := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, queriesPath) {
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"rows_affected": 1}}})
			return
		}
		mutex.Lock()
		served++
		first := served == 1
		mutex.Unlock()
		if !first {
			http.Error(w, "database is locked", http.StatusInternalServerError)
			return
		}
		encoder := json.NewEncoder(w)
		_ = encoder.Encode(map[string]any{"columns": []string{"id"}})
		_ = encoder.Encode(map[string]any{"row": []any{1, []any{id}}})
		_ = encoder.Encode(map[string]any{"eoq": map[string]any{"time": 0.0}})
	}))
	t.Cleanup(server.Close)
	return NewCatalog(server.URL, server.Client())
}

func TestTheProbeFailsWhereTheGapReadIsRefused(t *testing.T) {
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), writeOnlyCatalog(t))

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfOneFile)); err == nil {
		t.Error("the probe reported no error, want the refused read's")
	}
}

func TestTheIdentityFactFailsWhereAnItemReadIsRefused(t *testing.T) {
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), oneGapThenRefuses(t, "movie:path:x"))
	client, _ := newFakeTMDb(t, nil)

	if err := work.identityGap(t.Context(), client); err == nil {
		t.Error("the fact reported no error, want the refused read's")
	}
}

func TestAFolderThatWillNotTakeALedgerLogsTheFailure(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Thing (1982)")
	writeFile(t, filepath.Join(folder, likenDirectory), "a file where the directory belongs")
	work, log := testEnricher(t, libraryKindMovies, root, nil)

	work.recordIdentity(folder, nil, attemptNothing)

	if !strings.Contains(log.String(), "could not record") {
		t.Errorf("log = %q, want the line that names the failed record", log.String())
	}
}

func TestTheEnrichJobTakesItsOwnStartWhereItCannotReadTheRuns(t *testing.T) {
	run, _ := enrichJob(t, writeOnlyCatalog(t))

	if got := run.startedAt(t.Context()); got.IsZero() {
		t.Error("the container read no start at all, want its own")
	}
}

func TestAWriteThatCannotRenameLeavesNoTemporary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "movie.nfo")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "held"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := newVolumeWriter("movies-enrich").write(target, []byte("<movie/>")); err == nil {
		t.Fatal("the write reported no error, want the rename's")
	}
	for _, name := range namesIn(t, dir) {
		if strings.Contains(name, likenTempMark) {
			t.Errorf("the directory still holds %s", name)
		}
	}
}

func TestAWriteIntoAPathThatIsAFileFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "held"), "x")

	err := newVolumeWriter("movies-enrich").writeInto(filepath.Join(dir, "held", likenDirectory), "probe.yaml", []byte("x"))

	if err == nil {
		t.Error("the write reported no error, want one")
	}
}

func TestALedgerTheEnricherCannotReadIsAnError(t *testing.T) {
	folder := t.TempDir()
	if err := os.MkdirAll(filepath.Join(folder, likenDirectory, "probe.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := readLikenLedger(folder, factProbe); err == nil {
		t.Error("the read reported no error, want one")
	}
	err := newVolumeWriter("movies-enrich").updateLikenLedger(folder, factProbe, func(*likenLedger) {})
	if err == nil {
		t.Error("the update reported no error, want the read's")
	}
}

func TestAnIdThatIsNotAMappingIsAnError(t *testing.T) {
	folder := t.TempDir()
	writeFile(t, filepath.Join(folder, likenDirectory, "identity.yaml"), "items:\n  - path: .\n    id: [1, 2]\n")

	if _, err := readLikenLedger(folder, factIdentity); err == nil {
		t.Error("the read reported no error, want one")
	}
}

func TestAnElementTheDecoderCannotSkipFailsTheEdit(t *testing.T) {
	document := "<movie>\n  <fileinfo><a></fileinfo>\n</movie>\n"

	if _, err := editElement([]byte(document), xmlElement{name: "fileinfo"}, []byte("<NEW/>")); err == nil {
		t.Error("the edit reported no error, want one")
	}
}

// An agent that answers one read and refuses every read after it, so a test
// drives a reporter whose second count fails.
func oneCountThenRefuses(t *testing.T) *Catalog {
	t.Helper()
	var mutex sync.Mutex
	served := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		served++
		first := served == 1
		mutex.Unlock()
		if !first {
			http.Error(w, "database is locked", http.StatusInternalServerError)
			return
		}
		encoder := json.NewEncoder(w)
		_ = encoder.Encode(map[string]any{"columns": []string{"count"}})
		_ = encoder.Encode(map[string]any{"row": []any{1, []any{0.0}}})
		_ = encoder.Encode(map[string]any{"eoq": map[string]any{"time": 0.0}})
	}))
	t.Cleanup(server.Close)
	return NewCatalog(server.URL, server.Client())
}

func TestAGapCountThatIsRefusedIsAnError(t *testing.T) {
	if _, err := writeOnlyCatalog(t).gapCounts(t.Context(), "house/movies", ledgerTime); err == nil {
		t.Error("the gap count reported no error, want the refused read's")
	}
}

func TestAnIdentityCountThatIsRefusedIsAnError(t *testing.T) {
	cases := []struct {
		name    string
		catalog func(*testing.T) *Catalog
	}{
		{name: "the first count", catalog: writeOnlyCatalog},
		{name: "the second count", catalog: oneCountThenRefuses},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := test.catalog(t).identityCounts(t.Context(), "house/movies"); err == nil {
				t.Error("the counts reported no error, want the refused read's")
			}
		})
	}
}

func TestAnAttemptThatCannotBeRecordedLogsTheFailure(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Thing (1982)", "The Thing (1982).mkv")
	writeFile(t, filepath.Join(root, "The Thing (1982)", likenDirectory), "a file where the directory belongs")
	work, log := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfOneFile)); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(log.String(), "could not record the probe attempt") {
		t.Errorf("log = %q, want the line that names the failed record", log.String())
	}
}

func TestAnEnricherBuiltWithNoLogWritesNowhere(t *testing.T) {
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), nil)
	work.log = nil

	work.logf("this line goes nowhere")
}

func TestACooldownThatEndsOnTheContextIsAnError(t *testing.T) {
	client, fake := newFakeTMDb(t, nil)
	fake.tooMany = 1
	client.wait = func(context.Context, time.Duration) error { return context.Canceled }

	if _, err := client.search(t.Context(), libraryKindMovies, "The Thing", 1982); err == nil {
		t.Error("the search reported no error, want the cooldown's")
	}
}

func TestAnAddressTheClientCannotBuildIsAnError(t *testing.T) {
	client := newTMDbClient("http://\x7f", "a-token")

	if _, err := client.search(t.Context(), libraryKindMovies, "The Thing", 1982); err == nil {
		t.Error("the search reported no error, want one")
	}
}

func TestTheProbeStopsOnAShutdown(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Thing (1982)", "The Thing (1982).mkv")
	seedProbeGap(t, catalog, root, "Alien (1979)", "Alien (1979).mkv")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	ctx, cancel := context.WithCancel(t.Context())
	probe := func(context.Context, string) ([]byte, error) {
		cancel()
		return []byte(ffprobeOfOneFile), nil
	}

	if err := work.probeGap(ctx, probe); err == nil {
		t.Error("the probe reported no error, want the shutdown's")
	}
}

func TestTheIdentityFactStopsOnAShutdown(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seed := &walkResult{movies: []movieRow{
		{Id: "movie:path:a", Library: "house/movies", Kind: libraryKindMovies, Path: "A", Title: "A", Released: "1982"},
		{Id: "movie:path:b", Library: "house/movies", Kind: libraryKindMovies, Path: "B", Title: "B", Released: "1983"},
	}}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	client, _ := newFakeTMDb(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	client.wait = func(context.Context, time.Duration) error { return nil }
	client.http.Transport = cancellingTransport{cancel: cancel, inner: client.http.Transport}

	if err := work.identityGap(ctx, client); err == nil {
		t.Error("the fact reported no error, want the shutdown's")
	}
}

// Ends the run as soon as the provider is asked once, so a test drives the
// check a container makes between titles.
type cancellingTransport struct {
	cancel func()
	inner  http.RoundTripper
}

func (c cancellingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := c.inner.RoundTrip(request)
	c.cancel()
	return response, err
}

func TestAnItemRowWithTooFewColumnsIsSkipped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encoder := json.NewEncoder(w)
		_ = encoder.Encode(map[string]any{"columns": []string{"path", "title"}})
		_ = encoder.Encode(map[string]any{"row": []any{1, []any{"A", "A"}}})
		_ = encoder.Encode(map[string]any{"eoq": map[string]any{"time": 0.0}})
	}))
	t.Cleanup(server.Close)

	item, held, err := NewCatalog(server.URL, server.Client()).identityItem(t.Context(), "house/movies", "movie:path:a")
	if err != nil {
		t.Fatal(err)
	}
	if held || item.path != "" {
		t.Errorf("item = %+v, held = %v, want a row the reader left alone", item, held)
	}
}
