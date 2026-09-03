package main

// These tests run the write client against a small HTTP
// server that captures the posted transactions, so the statements, the
// parameterization, the 500-batching, and the errors are proved with no
// Corrosion agent.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// capturedStatement is one posted statement decoded back from the wire:
// the SQL and the parameters bound to it.
type capturedStatement struct {
	sql    string
	params []any
}

// catalogRecorder captures every posted batch and answers with a
// success result per statement, unless a test sets a status or a body.
type catalogRecorder struct {
	mu       sync.Mutex
	requests [][]capturedStatement
	status   int
	respBody string
}

func (rec *catalogRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A query reads from the catalog, which this recorder holds none of,
	// so it answers an empty stream: the columns marker and the
	// end-of-query marker, with no rows between them.
	if strings.HasSuffix(r.URL.Path, queriesPath) {
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"columns":["c"]}`+"\n"+`{"eoq":{"time":0.0}}`+"\n")
		return
	}
	body, _ := io.ReadAll(r.Body)
	statements := parseStatements(body)

	rec.mu.Lock()
	rec.requests = append(rec.requests, statements)
	status, override := rec.status, rec.respBody
	rec.mu.Unlock()

	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if override != "" {
		_, _ = io.WriteString(w, override)
		return
	}
	results := make([]map[string]any, len(statements))
	for i := range results {
		results[i] = map[string]any{"rows_affected": 1}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
}

// The batches this recorder captured, flattened to one list.
func (rec *catalogRecorder) all() []capturedStatement {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var out []capturedStatement
	for _, batch := range rec.requests {
		out = append(out, batch...)
	}
	return out
}

// parseStatements decodes a posted body of [sql, [params...]] entries.
func parseStatements(body []byte) []capturedStatement {
	var raw []json.RawMessage
	_ = json.Unmarshal(body, &raw)
	out := make([]capturedStatement, len(raw))
	for i, entry := range raw {
		var pair []json.RawMessage
		_ = json.Unmarshal(entry, &pair)
		var sql string
		_ = json.Unmarshal(pair[0], &sql)
		var params []any
		_ = json.Unmarshal(pair[1], &params)
		out[i] = capturedStatement{sql: sql, params: params}
	}
	return out
}

func testCatalog(t *testing.T, rec *catalogRecorder) *Catalog {
	t.Helper()
	server := httptest.NewServer(rec)
	t.Cleanup(server.Close)
	return NewCatalog(server.URL, server.Client())
}

func TestUpsertMoviesPostsAParameterizedUpsert(t *testing.T) {
	rec := &catalogRecorder{}
	catalog := testCatalog(t, rec)

	row := movieRow{
		Id: "movie:tmdb:603", Library: "house/movies", Kind: "movies",
		Path: "The Matrix (1999)", Title: "The Matrix", SortKey: "Matrix",
		Slug: "the-matrix-1999", Released: "1999", Added: 1700000000, Art: "folder.jpg", Duration: 8160,
		Body:  movieBody{Plot: "A hacker learns the truth."},
		SetID: "set:tmdb:2344",
	}
	applied, err := catalog.UpsertMovies(context.Background(), []movieRow{row})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1", applied)
	}

	statements := rec.all()
	if len(statements) != 1 {
		t.Fatalf("statements = %d, want 1", len(statements))
	}
	got := statements[0]
	if !strings.Contains(got.sql, "INSERT INTO movies") || !strings.Contains(got.sql, "ON CONFLICT (library, id) DO UPDATE SET") {
		t.Errorf("sql = %q, want an upsert on movies keyed by the library and the id", got.sql)
	}
	// A change to a primary-key column is a delete and a create in
	// cr-sqlite, so the update names no key column.
	for _, column := range []string{"library = excluded.library", "id = excluded.id"} {
		if strings.Contains(got.sql, column) {
			t.Errorf("sql = %q, want no update of a primary-key column", got.sql)
		}
	}
	// Every value is a parameter, so the id and the title never appear
	// in the SQL text.
	if strings.Contains(got.sql, "603") || strings.Contains(got.sql, "Matrix") {
		t.Errorf("sql = %q, want no values concatenated in", got.sql)
	}
	if len(got.params) != 15 {
		t.Fatalf("params = %d, want 15", len(got.params))
	}
	if got.params[14] != "[]" {
		t.Errorf("params[14] = %v, want an empty arts list", got.params[14])
	}
	if got.params[0] != "house/movies" {
		t.Errorf("params[0] = %v, want the library", got.params[0])
	}
	if got.params[1] != "movie:tmdb:603" {
		t.Errorf("params[1] = %v, want the id", got.params[1])
	}
	body, ok := got.params[10].(string)
	if !ok || !strings.Contains(body, "A hacker learns the truth.") {
		t.Errorf("params[10] = %v, want the marshaled body", got.params[10])
	}
	if got.params[11] != "the-matrix-1999" {
		t.Errorf("params[11] = %v, want the slug", got.params[11])
	}
	if got.params[12] != "set:tmdb:2344" {
		t.Errorf("params[12] = %v, want the set the movie belongs to", got.params[12])
	}
}

func TestUpsertBatchesAtFiveHundred(t *testing.T) {
	rec := &catalogRecorder{}
	catalog := testCatalog(t, rec)

	rows := make([]movieRow, 1201)
	for i := range rows {
		rows[i] = movieRow{Id: "movie:path:" + strconv.Itoa(i)}
	}
	applied, err := catalog.UpsertMovies(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 1201 {
		t.Errorf("applied = %d, want 1201", applied)
	}

	rec.mu.Lock()
	sizes := []int{}
	for _, batch := range rec.requests {
		sizes = append(sizes, len(batch))
	}
	rec.mu.Unlock()
	want := []int{500, 500, 201}
	if len(sizes) != len(want) {
		t.Fatalf("batch sizes = %v, want %v", sizes, want)
	}
	for i := range want {
		if sizes[i] != want[i] {
			t.Errorf("batch sizes = %v, want %v", sizes, want)
			break
		}
	}
}

func TestUpsertSeriesTargetsTheSeriesTable(t *testing.T) {
	rec := &catalogRecorder{}
	catalog := testCatalog(t, rec)

	_, err := catalog.UpsertSeries(context.Background(), []seriesRow{{Id: "series:tvdb:81189", Slug: "breaking-bad-2008"}})
	if err != nil {
		t.Fatal(err)
	}
	got := rec.all()[0]
	if !strings.Contains(got.sql, "INSERT INTO series") {
		t.Errorf("sql = %q, want an upsert on series", got.sql)
	}
	if len(got.params) != 14 {
		t.Errorf("params = %d, want 14", len(got.params))
	}
}

func TestUpsertEpisodesCarriesTheSeriesColumns(t *testing.T) {
	rec := &catalogRecorder{}
	catalog := testCatalog(t, rec)

	_, err := catalog.UpsertEpisodes(context.Background(), []episodeRow{{
		Id: "episode:tvdb:81189:s02e05", Series: "series:tvdb:81189", Season: 2, Episode: 5,
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := rec.all()[0]
	if !strings.Contains(got.sql, "INSERT INTO episodes") || !strings.Contains(got.sql, "series = excluded.series") {
		t.Errorf("sql = %q, want an upsert on episodes with the series columns", got.sql)
	}
	if len(got.params) != 16 {
		t.Fatalf("params = %d, want 16", len(got.params))
	}
	if got.params[12] != "series:tvdb:81189" {
		t.Errorf("params[12] = %v, want the series id", got.params[12])
	}
	if got.params[13].(float64) != 2 || got.params[14].(float64) != 5 {
		t.Errorf("season/episode params = %v/%v, want 2/5", got.params[13], got.params[14])
	}
}

func TestUpsertFilesCarriesPresentAsAnInteger(t *testing.T) {
	cases := []struct {
		name    string
		present bool
		want    float64
	}{
		{name: "present", present: true, want: 1},
		{name: "absent", present: false, want: 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rec := &catalogRecorder{}
			catalog := testCatalog(t, rec)

			_, err := catalog.UpsertFiles(context.Background(), []fileRow{{Path: "a.mkv", Present: testCase.present}})
			if err != nil {
				t.Fatal(err)
			}
			got := rec.all()[0]
			if !strings.Contains(got.sql, "INSERT INTO files") {
				t.Errorf("sql = %q, want an upsert on files", got.sql)
			}
			if len(got.params) != 15 {
				t.Fatalf("params = %d, want 15", len(got.params))
			}
			if got.params[10].(float64) != testCase.want {
				t.Errorf("present param = %v, want %v", got.params[10], testCase.want)
			}
		})
	}
}

// The type, the role, the language, and the modification time go through the
// same parameterized upsert as the rest of a file row, so a re-walk that
// reads an unchanged file writes an unchanged row.
func TestUpsertFilesCarriesTheClassification(t *testing.T) {
	rec := &catalogRecorder{}
	catalog := testCatalog(t, rec)

	row := fileRow{
		Path:     "One/One.en.forced.srt",
		Type:     fileTypeSubtitle,
		Role:     fileRoleForced,
		Language: "en",
		Modified: 1700000000,
	}
	if _, err := catalog.UpsertFiles(context.Background(), []fileRow{row}); err != nil {
		t.Fatal(err)
	}

	got := rec.all()[0]
	for _, column := range []string{"type", "role", "language", "modified"} {
		if !strings.Contains(got.sql, column+" = excluded."+column) {
			t.Errorf("sql = %q, want it to update %s in place", got.sql, column)
		}
	}
	if got.params[11] != fileTypeSubtitle || got.params[12] != fileRoleForced || got.params[13] != "en" {
		t.Errorf("params = %v, want the type, role, and language bound", got.params[11:14])
	}
	if got.params[14].(float64) != 1700000000 {
		t.Errorf("modified param = %v, want the Unix seconds", got.params[14])
	}
}

func TestUpsertFileItemsExpandsEveryPair(t *testing.T) {
	rec := &catalogRecorder{}
	catalog := testCatalog(t, rec)

	rows := []fileRow{{Path: "s01e01e02.mkv", Items: []string{"episode:tvdb:81189:s01e01", "episode:tvdb:81189:s01e02"}}}
	applied, err := catalog.UpsertFileItems(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 2 {
		t.Errorf("applied = %d, want 2", applied)
	}
	statements := rec.all()
	if len(statements) != 2 {
		t.Fatalf("statements = %d, want 2", len(statements))
	}
	// Every column of file_items is a key column, so the write has
	// nothing to update and a repeat changes no row.
	if !strings.Contains(statements[0].sql, "INSERT INTO file_items") ||
		!strings.Contains(statements[0].sql, "ON CONFLICT (library, path, item) DO NOTHING") {
		t.Errorf("sql = %q, want an insert on file_items that does nothing on a conflict", statements[0].sql)
	}
	if statements[0].params[1] != "s01e01e02.mkv" || statements[1].params[2] != "episode:tvdb:81189:s01e02" {
		t.Errorf("params = %v / %v, want the path and each item", statements[0].params, statements[1].params)
	}
}

func TestUpsertAliasesWritesEachName(t *testing.T) {
	rec := &catalogRecorder{}
	catalog := testCatalog(t, rec)

	_, err := catalog.UpsertAliases(context.Background(), []aliasRow{{
		Alias: "movie:imdb:tt0133093", Library: "house/movies", Item: "movie:tmdb:603", Source: aliasSourceProvider,
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := rec.all()[0]
	if !strings.Contains(got.sql, "INSERT INTO aliases") || !strings.Contains(got.sql, "ON CONFLICT (library, alias)") {
		t.Errorf("sql = %q, want an upsert on aliases keyed by the library and the alias", got.sql)
	}
	if got.params[0] != "house/movies" || got.params[1] != "movie:imdb:tt0133093" ||
		got.params[2] != "movie:tmdb:603" || got.params[3] != aliasSourceProvider {
		t.Errorf("params = %v, want the library, alias, item, and source", got.params)
	}
}

func TestDeleteByKeyMethods(t *testing.T) {
	cases := []struct {
		name   string
		delete func(*Catalog, context.Context) (int, error)
		want   string
	}{
		{name: "movies", delete: func(c *Catalog, ctx context.Context) (int, error) {
			return c.DeleteMovies(ctx, "house/movies", []string{"movie:tmdb:603"})
		}, want: "DELETE FROM movies WHERE library = ? AND id = ?"},
		{name: "series", delete: func(c *Catalog, ctx context.Context) (int, error) {
			return c.DeleteSeries(ctx, "house/movies", []string{"series:tvdb:81189"})
		}, want: "DELETE FROM series WHERE library = ? AND id = ?"},
		{name: "episodes", delete: func(c *Catalog, ctx context.Context) (int, error) {
			return c.DeleteEpisodes(ctx, "house/movies", []string{"episode:tvdb:81189:s01e01"})
		}, want: "DELETE FROM episodes WHERE library = ? AND id = ?"},
		{name: "files", delete: func(c *Catalog, ctx context.Context) (int, error) {
			return c.DeleteFiles(ctx, "house/movies", []string{"a.mkv"})
		}, want: "DELETE FROM files WHERE library = ? AND path = ?"},
		{name: "aliases", delete: func(c *Catalog, ctx context.Context) (int, error) {
			return c.DeleteAliases(ctx, "house/movies", []string{"movie:imdb:tt0133093"})
		}, want: "DELETE FROM aliases WHERE library = ? AND alias = ?"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rec := &catalogRecorder{}
			catalog := testCatalog(t, rec)

			applied, err := testCase.delete(catalog, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if applied != 1 {
				t.Errorf("applied = %d, want 1", applied)
			}
			got := rec.all()[0]
			if got.sql != testCase.want {
				t.Errorf("sql = %q, want %q", got.sql, testCase.want)
			}
			// The library is the first parameter and the row's own key
			// follows it, the convention every statement holds to.
			if len(got.params) != 2 || got.params[0] != "house/movies" {
				t.Errorf("params = %v, want the library and one key", got.params)
			}
		})
	}
}

func TestDeleteFileItemsNamesEveryKeyColumn(t *testing.T) {
	rec := &catalogRecorder{}
	catalog := testCatalog(t, rec)

	links := []fileItemKey{
		{Path: "s01e01e02.mkv", Item: "episode:tvdb:81189:s01e01"},
		{Path: "s01e01e02.mkv", Item: "episode:tvdb:81189:s01e02"},
	}
	_, err := catalog.DeleteFileItems(context.Background(), "house/series", links)
	if err != nil {
		t.Fatal(err)
	}
	statements := rec.all()
	if len(statements) != 2 {
		t.Fatalf("statements = %d, want 2", len(statements))
	}
	if statements[0].sql != "DELETE FROM file_items WHERE library = ? AND path = ? AND item = ?" {
		t.Errorf("sql = %q, want a delete on file_items by all three key columns", statements[0].sql)
	}
	if statements[0].params[0] != "house/series" || statements[0].params[1] != "s01e01e02.mkv" ||
		statements[1].params[2] != "episode:tvdb:81189:s01e02" {
		t.Errorf("params = %v / %v, want the library, the path, and each item", statements[0].params, statements[1].params)
	}
}

func TestEmptyInputPostsNothing(t *testing.T) {
	rec := &catalogRecorder{}
	catalog := testCatalog(t, rec)

	applied, err := catalog.UpsertMovies(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 0 {
		t.Errorf("applied = %d, want 0", applied)
	}
	if len(rec.requests) != 0 {
		t.Errorf("requests = %d, want none", len(rec.requests))
	}
}

func TestPostSurfacesANon2xxStatus(t *testing.T) {
	rec := &catalogRecorder{status: http.StatusInternalServerError, respBody: "the agent failed"}
	catalog := testCatalog(t, rec)

	applied, err := catalog.UpsertMovies(context.Background(), []movieRow{{Id: "movie:tmdb:603"}})
	if err == nil {
		t.Fatal("err = nil, want the agent's status")
	}
	if !strings.Contains(err.Error(), "the agent failed") {
		t.Errorf("err = %v, want it to carry the message", err)
	}
	if applied != 0 {
		t.Errorf("applied = %d, want 0", applied)
	}
}

func TestPostSurfacesAStatementError(t *testing.T) {
	rec := &catalogRecorder{respBody: `{"results":[{"error":"no such column"}]}`}
	catalog := testCatalog(t, rec)

	_, err := catalog.UpsertMovies(context.Background(), []movieRow{{Id: "movie:tmdb:603"}})
	if err == nil || !strings.Contains(err.Error(), "no such column") {
		t.Fatalf("err = %v, want the statement error", err)
	}
}

func TestPostSurfacesADecodeError(t *testing.T) {
	rec := &catalogRecorder{respBody: "not json"}
	catalog := testCatalog(t, rec)

	_, err := catalog.UpsertMovies(context.Background(), []movieRow{{Id: "movie:tmdb:603"}})
	if err == nil || !strings.Contains(err.Error(), "decoding response") {
		t.Fatalf("err = %v, want a decode error", err)
	}
}

func TestPostSurfacesATransportError(t *testing.T) {
	rec := &catalogRecorder{}
	server := httptest.NewServer(rec)
	catalog := NewCatalog(server.URL, server.Client())
	server.Close()

	_, err := catalog.UpsertMovies(context.Background(), []movieRow{{Id: "movie:tmdb:603"}})
	if err == nil {
		t.Fatal("err = nil, want a transport error")
	}
}

func TestPostSurfacesABadBaseURL(t *testing.T) {
	catalog := NewCatalog("http://\x7f", http.DefaultClient)

	_, err := catalog.UpsertMovies(context.Background(), []movieRow{{Id: "movie:tmdb:603"}})
	if err == nil {
		t.Fatal("err = nil, want a request-construction error")
	}
}

// The agent answers one result per statement. A body with fewer results
// describes a batch the agent did not run in full, so the write fails
// rather than reporting the rows the short answer happened to carry.
func TestAShortTransactionAnswerIsAFailure(t *testing.T) {
	catalog, recorder := recordingCatalog(t)
	recorder.respBody = `{"results":[{"rows_affected":1}]}`

	applied, err := catalog.UpsertMovies(t.Context(), []movieRow{
		{Id: "movie:tmdb:1", Library: "house/movies", Path: "One"},
		{Id: "movie:tmdb:2", Library: "house/movies", Path: "Two"},
	})

	if err == nil {
		t.Error("a short transaction answer read as success")
	}
	if applied != 0 {
		t.Errorf("applied = %d, want none from a batch the agent did not answer for", applied)
	}
}
