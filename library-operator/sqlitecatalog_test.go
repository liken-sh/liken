package main

// This file runs the scanner's own SQL against a real SQLite database
// loaded with the shipped schema, so the mark-and-sweep is proved against
// the column names, the key spaces, and the joins the agent holds, and
// not against a fake that reads the statements by keyword. A query that
// names a column the schema does not have fails here.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// catalogSchemaPath is the schema every agent loads, from the image and
// from the local harness. The tests read the same file.
const catalogSchemaPath = "corrosion/schema/catalog.sql"

// sqliteAgent answers the two endpoints a real Corrosion agent answers,
// against a SQLite database in memory. The scanner writes through the
// transaction endpoint and reads through the query endpoint, so a whole
// prune runs here with no agent and no cr-sqlite.
type sqliteAgent struct {
	db *sql.DB

	// The mutex covers what the endpoints share, because the agent
	// serves the transaction, the query, the subscription, and the
	// update streams at the same time.
	mutex sync.Mutex
	// the most statements one posted transaction carried, so a test reads the
	// bound a chunked sweep keeps.
	largestBatch int
	// One entry per open update stream, each waiting on the
	// writes that name its table.
	watchers []tableWatcher
}

// newSQLiteCatalog opens a database with the shipped schema, serves it
// over the agent's two endpoints, and hands back the client the scanner
// writes through.
func newSQLiteCatalog(t *testing.T) (*Catalog, *sqliteAgent) {
	t.Helper()
	// One connection, because an in-memory database belongs to the
	// connection that opened it.
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	schema, err := os.ReadFile(catalogSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	// The driver runs a whole script in one Exec when it binds no
	// parameters, which is how the agent loads this file too.
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("loading the schema: %v", err)
	}

	agent := &sqliteAgent{db: db}
	server := httptest.NewServer(agent)
	t.Cleanup(server.Close)
	return NewCatalog(server.URL, server.Client()), agent
}

func (a *sqliteAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, queriesPath) {
		a.serveQuery(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, subscriptionsPath) {
		a.serveSubscription(w, r)
		return
	}
	if at := strings.Index(r.URL.Path, updatesPath); at >= 0 {
		a.serveUpdates(w, r, r.URL.Path[at+len(updatesPath):])
		return
	}
	a.serveTransaction(w, r)
}

// Answers a subscription the way the agent does: the columns, the
// rows the query holds now, the end of that snapshot, and then one change
// per row that appears or moves after it, until the caller goes away.
func (a *sqliteAgent) serveSubscription(w http.ResponseWriter, r *http.Request) {
	query, _ := parseQuery(readBody(r))
	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)

	columns, rows, err := a.readAll(query)
	if err != nil {
		_ = enc.Encode(map[string]any{"error": err.Error()})
		return
	}
	_ = enc.Encode(map[string]any{"columns": columns})
	held := map[string]bool{}
	for number, cells := range rows {
		held[fmt.Sprint(cells)] = true
		_ = enc.Encode(map[string]any{"row": []any{number + 1, cells}})
	}
	_ = enc.Encode(map[string]any{"eoq": map[string]any{"time": 0.0}})
	flusher.Flush()

	for r.Context().Err() == nil {
		time.Sleep(time.Millisecond)
		_, rows, err := a.readAll(query)
		if err != nil {
			return
		}
		for number, cells := range rows {
			key := fmt.Sprint(cells)
			if held[key] {
				continue
			}
			held[key] = true
			_ = enc.Encode(map[string]any{"change": []any{"update", number + 1, cells, len(held)}})
			flusher.Flush()
		}
	}
}

// Reads one query into its columns and every row's cells.
func (a *sqliteAgent) readAll(query string) ([]string, [][]any, error) {
	rows, err := a.db.Query(query)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	columns, _ := rows.Columns()
	var out [][]any
	for rows.Next() {
		cells := make([]any, len(columns))
		into := make([]any, len(columns))
		for i := range cells {
			into[i] = &cells[i]
		}
		if err := rows.Scan(into...); err != nil {
			return nil, nil, err
		}
		out = append(out, cells)
	}
	return columns, out, rows.Err()
}

// serveTransaction runs each posted statement and answers with one
// result per statement, the shape the write client reads.
func (a *sqliteAgent) serveTransaction(w http.ResponseWriter, r *http.Request) {
	statements := parseStatements(readBody(r))
	a.mutex.Lock()
	a.largestBatch = max(a.largestBatch, len(statements))
	a.mutex.Unlock()
	defer a.notifyWatchers(statements)
	results := make([]map[string]any, len(statements))
	for i, s := range statements {
		outcome, err := a.db.Exec(s.sql, s.params...)
		if err != nil {
			results[i] = map[string]any{"error": err.Error()}
			continue
		}
		affected, _ := outcome.RowsAffected()
		results[i] = map[string]any{"rows_affected": affected}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
}

// serveQuery runs one read and streams the events the agent streams:
// the columns, one row event per row, then the end-of-query marker.
func (a *sqliteAgent) serveQuery(w http.ResponseWriter, r *http.Request) {
	query, params := parseQuery(readBody(r))
	rows, err := a.db.Query(query, params...)
	enc := json.NewEncoder(w)
	if err != nil {
		_ = enc.Encode(map[string]any{"error": err.Error()})
		return
	}
	defer rows.Close()

	columns, _ := rows.Columns()
	_ = enc.Encode(map[string]any{"columns": columns})
	number := 0
	for rows.Next() {
		cells := make([]any, len(columns))
		into := make([]any, len(columns))
		for i := range cells {
			into[i] = &cells[i]
		}
		if err := rows.Scan(into...); err != nil {
			_ = enc.Encode(map[string]any{"error": err.Error()})
			return
		}
		number++
		_ = enc.Encode(map[string]any{"row": []any{number, cells}})
	}
	_ = enc.Encode(map[string]any{"eoq": map[string]any{"time": 0.0}})
}

// rowCount reads how many rows one table holds, so a test reads the
// survivors of a sweep.
func (a *sqliteAgent) rowCount(t *testing.T, table string) int {
	t.Helper()
	count := 0
	if err := a.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// holdsItem reports whether one library still holds one item id. It
// names both, because an id identifies a row only inside its own
// library.
func (a *sqliteAgent) holdsItem(t *testing.T, table, library, id string) bool {
	t.Helper()
	count := 0
	if err := a.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE library = ? AND id = ?`,
		library, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count > 0
}

// rowsFor reads how many rows of one table one library holds, the
// count a cross-library test reads to prove that one library's prune
// left the other library whole.
func (a *sqliteAgent) rowsFor(t *testing.T, table, library string) int {
	t.Helper()
	count := 0
	if err := a.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE library = ?`, library).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// walkOfOneTitle is the rows one movie title folder produces: the item,
// one video file, the link between them, and the two aliases a provider
// id and a folder key give it.
func walkOfOneTitle(library, id, path, folderKey string) *walkResult {
	file := path + "/movie.mkv"
	return &walkResult{
		movies: []movieRow{{
			Id: id, Library: library, Kind: libraryKindMovies, Path: path, Title: id,
		}},
		files: []fileRow{{
			Path: file, Library: library, Present: true, Type: fileTypeVideo,
			Role: fileRolePrimary, Items: []string{id},
		}},
		aliases: []aliasRow{
			{Alias: id, Library: library, Item: id, Source: aliasSourceProvider},
			{Alias: folderKey, Library: library, Item: id, Source: aliasSourceFolder},
		},
		genres: []genreRow{{Library: library, Item: id, Rank: 0, Genre: "Western"}},
		titles: 1,
	}
}

// A full walk writes what it read and marks it, and the prune deletes
// the rows of the title the walk no longer found, in every table: the
// item, its file, the link between them, and its aliases. The other
// library's rows are out of scope and stand.
func TestPruneLibraryAgainstTheRealSchema(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}

	first := time.Now().Add(-time.Hour).UnixNano()
	for _, walk := range []*walkResult{
		walkOfOneTitle("house/movies", "movie:tmdb:1", "One (2001)", "movie:path:one-2001"),
		walkOfOneTitle("house/movies", "movie:tmdb:2", "Two (2002)", "movie:path:two-2002"),
		walkOfOneTitle("studio/films", "movie:tmdb:9", "Nine (2009)", "movie:path:nine-2009"),
	} {
		if err := flushWalk(ctx, catalog, walk, first); err != nil {
			t.Fatal(err)
		}
	}
	if got := agent.rowCount(t, "movies"); got != 3 {
		t.Fatalf("movies = %d, want the three seeded titles", got)
	}

	// The second walk of this library read the first title alone, so the
	// second title's rows carry the old epoch and leave.
	second := time.Now().UnixNano()
	if err := flushWalk(ctx, catalog, walkOfOneTitle("house/movies", "movie:tmdb:1", "One (2001)", "movie:path:one-2001"), second); err != nil {
		t.Fatal(err)
	}

	removed, err := pruneLibrary(ctx, catalog, "house/movies", second)
	if err != nil {
		t.Fatal(err)
	}
	// The departed title's item, its file, its link, its two aliases, and its genre.
	if removed != 6 {
		t.Errorf("removed = %d, want the departed title's six rows", removed)
	}
	if !agent.holdsItem(t, "movies", "house/movies", "movie:tmdb:1") {
		t.Error("the marked title was swept")
	}
	if agent.holdsItem(t, "movies", "house/movies", "movie:tmdb:2") {
		t.Error("the unmarked title stands")
	}
	if !agent.holdsItem(t, "movies", "studio/films", "movie:tmdb:9") {
		t.Error("the other library's title was swept")
	}
	if got := agent.rowCount(t, "files"); got != 2 {
		t.Errorf("files = %d, want the marked file and the other library's", got)
	}
	if got := agent.rowCount(t, "file_items"); got != 2 {
		t.Errorf("file_items = %d, want the links of the two surviving files", got)
	}
	if got := agent.rowCount(t, "aliases"); got != 4 {
		t.Errorf("aliases = %d, want the two aliases of each surviving title", got)
	}
	// The prune cleans the marks behind the current epoch, so the seen
	// table tracks the live catalog.
	if got := agent.rowCount(t, "seen"); got != 6 {
		t.Errorf("seen = %d, want only the marks of the current epoch", got)
	}
	// Every prune query reads the ids of one epoch, so the seen table
	// carries the index that read leads with.
	if !agent.holdsIndex(t, "seen_epoch") {
		t.Error("the seen table carries no index on epoch")
	}
}

// holdsIndex reports whether the database carries one index by name.
func (a *sqliteAgent) holdsIndex(t *testing.T, name string) bool {
	t.Helper()
	count := 0
	if err := a.db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count > 0
}

// A rescan reconciles one folder: the rows under it that the rescan did
// not mark leave, and every row outside it stands, whatever characters
// the folder name holds. The scope is a range over the path, so a name
// with a LIKE metacharacter in it needs no escape.
func TestPruneScopeAgainstTheRealSchema(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}

	first := time.Now().Add(-time.Hour).UnixNano()
	for _, walk := range []*walkResult{
		walkOfOneTitle("house/movies", "movie:tmdb:1", "100% Wolf (2020)", "movie:path:100-wolf-2020"),
		walkOfOneTitle("house/movies", "movie:tmdb:2", "100 Bullets (2019)", "movie:path:100-bullets-2019"),
	} {
		if err := flushWalk(ctx, catalog, walk, first); err != nil {
			t.Fatal(err)
		}
	}

	// The folder left the volume, so the rescan marks nothing and every
	// row under it is unmarked.
	removed, err := pruneScope(ctx, catalog, "house/movies", "100% Wolf (2020)", time.Now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 6 {
		t.Errorf("removed = %d, want the folder's six rows", removed)
	}
	if agent.holdsItem(t, "movies", "house/movies", "movie:tmdb:1") {
		t.Error("the departed folder's title stands")
	}
	if !agent.holdsItem(t, "movies", "house/movies", "movie:tmdb:2") {
		t.Error("a title outside the folder was swept")
	}
	if got := agent.rowCount(t, "files"); got != 1 {
		t.Errorf("files = %d, want the file outside the folder", got)
	}
	if got := agent.rowCount(t, "aliases"); got != 2 {
		t.Errorf("aliases = %d, want the aliases outside the folder", got)
	}
}

// The alias and item key spaces stay separate under one prune, so a
// title that gained a provider id loses its old path-derived item row
// and keeps the alias that resolves the old id to the new one.
func TestTheKeySpacesSeparateAnAliasFromAnItemAgainstTheRealSchema(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}

	// The first walk read the folder with no sidecar, so its id is the
	// folder key.
	if err := flushWalk(ctx, catalog, walkOfOneTitle("house/movies", "movie:path:one-2001", "One (2001)", "movie:path:one-2001"), time.Now().Add(-time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}

	// The sidecar arrived, so the title's id is now the provider id and
	// the old id is one of its aliases.
	second := time.Now().UnixNano()
	if err := flushWalk(ctx, catalog, walkOfOneTitle("house/movies", "movie:tmdb:1", "One (2001)", "movie:path:one-2001"), second); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneLibrary(ctx, catalog, "house/movies", second); err != nil {
		t.Fatal(err)
	}

	if agent.holdsItem(t, "movies", "house/movies", "movie:path:one-2001") {
		t.Error("the stale path-derived item stands, so the catalog holds the title twice")
	}
	if !agent.holdsItem(t, "movies", "house/movies", "movie:tmdb:1") {
		t.Error("the provider-derived item was swept")
	}
	if got := agent.rowCount(t, "aliases"); got != 2 {
		t.Errorf("aliases = %d, want the provider id and the folder key", got)
	}
}

// The namespace's tables hold every library, so two libraries can carry a
// title under the same provider id at the same relative path. The library
// leads every key, so the two are two rows in every table and neither
// walk overwrites the other.
func TestTwoLibrariesHoldTheSameIdAndPathAgainstTheRealSchema(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}

	for _, library := range []string{"house/movies", "studio/films"} {
		walk := walkOfOneTitle(library, "movie:tmdb:1", "One (2001)", "movie:path:one-2001")
		if err := flushWalk(ctx, catalog, walk, time.Now().Add(-time.Hour).UnixNano()); err != nil {
			t.Fatal(err)
		}
	}

	for _, table := range []struct {
		name string
		want int
	}{
		{"movies", 1}, {"files", 1}, {"file_items", 1}, {"aliases", 2},
	} {
		for _, library := range []string{"house/movies", "studio/films"} {
			if got := agent.rowsFor(t, table.name, library); got != table.want {
				t.Errorf("%s of %s = %d, want %d", table.name, library, got, table.want)
			}
		}
		if got := agent.rowCount(t, table.name); got != 2*table.want {
			t.Errorf("%s = %d, want the rows of both libraries", table.name, got)
		}
	}
}

// seedTwoLibraries writes the same two titles into two libraries, at the
// same relative paths and under the same provider ids, and marks them all
// with one epoch.
func seedTwoLibraries(t *testing.T, catalog *Catalog, epoch int64) {
	t.Helper()
	for _, library := range []string{"house/movies", "studio/films"} {
		for _, title := range []struct{ id, path, key string }{
			{"movie:tmdb:1", "One (2001)", "movie:path:one-2001"},
			{"movie:tmdb:2", "Two (2002)", "movie:path:two-2002"},
		} {
			walk := walkOfOneTitle(library, title.id, title.path, title.key)
			if err := flushWalk(t.Context(), catalog, walk, epoch); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// libraryRows is the row count of each table one seeded library holds, the
// shape a cross-library test reads back after the other library's prune.
func libraryRows(t *testing.T, agent *sqliteAgent, library string) map[string]int {
	t.Helper()
	rows := map[string]int{}
	for _, table := range []string{"movies", "files", "file_items", "aliases"} {
		rows[table] = agent.rowsFor(t, table, library)
	}
	return rows
}

// A full walk and its prune reach one library alone. The other library
// holds the same ids at the same paths, and every one of its rows stands.
func TestPruneLibraryLeavesTheOtherLibrarysIdenticalRows(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	seedTwoLibraries(t, catalog, time.Now().Add(-time.Hour).UnixNano())
	before := libraryRows(t, agent, "studio/films")

	// The next walk of this library read the first title alone, so the
	// second title's rows carry the old epoch and leave.
	second := time.Now().UnixNano()
	walk := walkOfOneTitle("house/movies", "movie:tmdb:1", "One (2001)", "movie:path:one-2001")
	if err := flushWalk(ctx, catalog, walk, second); err != nil {
		t.Fatal(err)
	}

	removed, err := pruneLibrary(ctx, catalog, "house/movies", second)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 6 {
		t.Errorf("removed = %d, want the departed title's six rows", removed)
	}
	if !agent.holdsItem(t, "movies", "house/movies", "movie:tmdb:1") {
		t.Error("the marked title was swept")
	}
	if agent.holdsItem(t, "movies", "house/movies", "movie:tmdb:2") {
		t.Error("the unmarked title stands")
	}
	after := libraryRows(t, agent, "studio/films")
	for table, want := range before {
		if after[table] != want {
			t.Errorf("%s of the other library = %d, want the %d it held before the prune", table, after[table], want)
		}
	}
}

// A rescan of one folder reaches one library alone. The other library
// holds a folder of the same name, and every row under it stands.
func TestPruneScopeLeavesTheOtherLibrarysFolder(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	seedTwoLibraries(t, catalog, time.Now().Add(-time.Hour).UnixNano())
	before := libraryRows(t, agent, "studio/films")

	// The folder left this library's volume, so the rescan marks nothing
	// and every row under it is unmarked.
	removed, err := pruneScope(ctx, catalog, "house/movies", "One (2001)", time.Now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 6 {
		t.Errorf("removed = %d, want the folder's six rows", removed)
	}
	if agent.holdsItem(t, "movies", "house/movies", "movie:tmdb:1") {
		t.Error("the departed folder's title stands")
	}
	if !agent.holdsItem(t, "movies", "house/movies", "movie:tmdb:2") {
		t.Error("a title outside the folder was swept")
	}
	if !agent.holdsItem(t, "movies", "studio/films", "movie:tmdb:1") {
		t.Error("the other library's folder of the same name was swept")
	}
	after := libraryRows(t, agent, "studio/films")
	for table, want := range before {
		if after[table] != want {
			t.Errorf("%s of the other library = %d, want the %d it held before the prune", table, after[table], want)
		}
	}
}
