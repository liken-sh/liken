package main

// This file runs the scanner's own SQL against a real SQLite database
// loaded with the shipped schema, so the mark-and-sweep is proved against
// the column names, the key spaces, and the joins the agent holds, and
// not against a fake that reads the statements by keyword. A query that
// names a column the schema does not have fails here.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	// How many reads, and how many writes, the agent answers before it refuses
	// every later one. Zero answers every request, and a positive number is what
	// a test sets to stop a walk or a sweep at one of its steps.
	queriesLeft      int
	transactionsLeft int
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
	if a.refusesWrite() {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
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
	if a.refusesRead() {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
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

// Whether this read is past the number the test allowed. A test that allowed
// none is never refused, so every other test reads the way it always did.
func (a *sqliteAgent) refusesRead() bool {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.queriesLeft == 0 {
		return false
	}
	a.queriesLeft--
	return a.queriesLeft == 0
}

// Whether this write is past the number the test allowed, read the way a
// refused read is.
func (a *sqliteAgent) refusesWrite() bool {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.transactionsLeft == 0 {
		return false
	}
	a.transactionsLeft--
	return a.transactionsLeft == 0
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

// A prune whose catalog stops answering stops where it stopped. It reports
// the error and the rows it removed up to that point, so the next walk
// finishes the sweep rather than reading a count that lies. Every step of the
// sweep answers the same way, whichever one fails.
func TestPruneLibraryStopsAtTheStepItsCatalogRefuses(t *testing.T) {
	// The sweep reads once per table plus the count of marks, and the
	// franchise tables are the last of them.
	for step := 1; step <= 16; step++ {
		t.Run(fmt.Sprintf("the read %d of the sweep", step), func(t *testing.T) {
			catalog, agent := newSQLiteCatalog(t)
			ctx := t.Context()
			epoch := seedOnePrunableTitle(t, catalog, agent)
			agent.queriesLeft = step

			removed, err := pruneLibrary(ctx, catalog, "house/movies", epoch)

			if err == nil {
				t.Fatalf("the prune removed %d rows and reported no error, want the refused read", removed)
			}
			if removed < 0 || removed > 6 {
				t.Errorf("removed = %d, want the rows it took before the read it could not make", removed)
			}
		})
	}
}

// One library with one title the next walk did not mark, and the epoch that
// walk carried. The other library's identical rows stand beside it, so a
// sweep that reaches too far is visible.
func seedOnePrunableTitle(t *testing.T, catalog *Catalog, agent *sqliteAgent) int64 {
	t.Helper()
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	first := time.Now().Add(-time.Hour).UnixNano()
	for _, walk := range []*walkResult{
		walkOfOneTitle("house/movies", "movie:tmdb:1", "One (2001)", "movie:path:one-2001"),
		walkOfOneTitle("house/movies", "movie:tmdb:2", "Two (2002)", "movie:path:two-2002"),
	} {
		if err := flushWalk(ctx, catalog, walk, first); err != nil {
			t.Fatal(err)
		}
	}
	second := time.Now().UnixNano()
	if err := flushWalk(ctx, catalog,
		walkOfOneTitle("house/movies", "movie:tmdb:1", "One (2001)", "movie:path:one-2001"),
		second); err != nil {
		t.Fatal(err)
	}
	if got := agent.rowCount(t, "movies"); got != 2 {
		t.Fatalf("movies = %d, want the two seeded titles", got)
	}
	return second
}

// A scoped prune whose catalog stops answering stops where it stopped, the
// way the whole-library sweep does. A webhook rescan then leaves the folder's
// rows for the next walk.
func TestPruneScopeStopsAtTheStepItsCatalogRefuses(t *testing.T) {
	for step := 1; step <= 7; step++ {
		t.Run(fmt.Sprintf("the read %d of the sweep", step), func(t *testing.T) {
			catalog, agent := newSQLiteCatalog(t)
			ctx := t.Context()
			epoch := seedOnePrunableTitle(t, catalog, agent)
			agent.queriesLeft = step

			removed, err := pruneScope(ctx, catalog, "house/movies", "Two (2002)", epoch)

			if err == nil {
				t.Fatalf("the prune removed %d rows and reported no error, want the refused read", removed)
			}
		})
	}
}

// The Job reads the commit and the failure its own last run left, so a scan
// of an unchanged repository writes no row. A library whose scan has never
// run reads both as empty.
func TestTheJobReadsTheCommitItsLastRunLeft(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.UpsertRun(ctx, "house/franchises", libraryRun{
		Worker: workerScan, Job: "franchises-scan-1",
		Commit: "9b1c0a7f2d3e4b5a6c7d8e9f0a1b2c3d4e5f6a7b", Failure: "the forge answered nothing",
	}); err != nil {
		t.Fatal(err)
	}

	held, err := catalog.lastScan(ctx, "house/franchises")
	if err != nil {
		t.Fatal(err)
	}
	none, err := catalog.lastScan(ctx, "house/movies")
	if err != nil {
		t.Fatal(err)
	}

	if held.Commit != "9b1c0a7f2d3e4b5a6c7d8e9f0a1b2c3d4e5f6a7b" {
		t.Errorf("commit = %q, want the one the last run read", held.Commit)
	}
	if held.Failure != "the forge answered nothing" {
		t.Errorf("failure = %q, want the one the last run left", held.Failure)
	}
	if none.Commit != "" || none.Failure != "" {
		t.Errorf("a library with no scan run reads %+v, want both empty", none)
	}
}

// A full walk whose catalog refuses a write fails the Job and prunes nothing,
// so the rows the last good walk left stand. Every write of the walk answers
// the same way, whichever one is refused.
func TestTheFullWalkFailsAndPrunesNothingOnARefusedWrite(t *testing.T) {
	for step := 1; step <= 4; step++ {
		t.Run(fmt.Sprintf("the write %d of the walk", step), func(t *testing.T) {
			scan, agent := sqliteScanner(t, titleTree(t, "One (2001)", "Two (2002)"))
			seeded := time.Now().Add(-time.Hour).UnixNano()
			if err := scan.catalog.ensureSeen(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := flushWalk(t.Context(), scan.catalog,
				walkOfOneTitle("house/movies", "movie:tmdb:9", "Nine (2009)", "movie:path:nine-2009"),
				seeded); err != nil {
				t.Fatal(err)
			}
			agent.transactionsLeft = step

			err := scan.fullWalk(t.Context())

			if err == nil {
				t.Fatal("the walk finished, want it failed on the refused write")
			}
			if !agent.holdsItem(t, "movies", "house/movies", "movie:tmdb:9") {
				t.Error("the seeded title was swept by a walk that could not write")
			}
		})
	}
}

// A rescan of one folder whose catalog refuses a step reports which step, so
// the pod log names it. The rescan writes no count when it could not finish,
// because a partial count would move the library's numbers.
func TestTheRescanReportsTheStepItsCatalogRefused(t *testing.T) {
	for step := 1; step <= 3; step++ {
		t.Run(fmt.Sprintf("the write %d of the rescan", step), func(t *testing.T) {
			root := titleTree(t, "One (2001)")
			scan, agent := sqliteScanner(t, root)
			agent.transactionsLeft = step

			_, _, err := rescanFolder(t.Context(), scan.catalog, scan.folderScan(),
				filepath.Join(root, "One (2001)"))

			if err == nil {
				t.Fatal("the rescan finished, want it failed on the refused write")
			}
		})
	}
}

// Two folders whose sidecars carry one provider id are one title in the
// catalog, because the id is the item's key. The walk reports the titles the
// catalog holds and the folders it read, so the number in the log is the
// number the Library's status carries.
func TestTheWalkReportsTheTitlesTheCatalogHoldsAndTheFoldersItRead(t *testing.T) {
	root := t.TempDir()
	for _, folder := range []string{"The Thing (1982)", "The Thing (1982) [4K]"} {
		writeFile(t, filepath.Join(root, folder, "movie.mkv"), "x")
		writeFile(t, filepath.Join(root, folder, "movie.nfo"),
			"<movie><title>The Thing</title><year>1982</year>"+
				"<uniqueid type=\"tmdb\">1091</uniqueid></movie>")
	}
	scan, agent := sqliteScanner(t, root)
	log := &bytes.Buffer{}
	scan.log = log

	if err := scan.fullWalk(t.Context()); err != nil {
		t.Fatal(err)
	}

	if held := agent.rowsFor(t, "movies", "house/movies"); held != 1 {
		t.Fatalf("movies holds %d rows, want the one title the two folders name", held)
	}
	if !strings.Contains(log.String(), "1 titles from 2 folders") {
		t.Errorf("the log reads %q, want the one title the catalog holds from the two folders",
			log.String())
	}
}

// A folder the scanner cannot stat is not a folder that left the volume. A
// rescan of it writes nothing and sweeps nothing, so a share that refuses one
// directory for a moment never empties a title's rows. The failure reaches
// the caller, which fails the Job and retries.
func TestARescanSweepsNothingForAFolderItCannotStat(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "One (2001)", "movie.mkv"), "x")
	scan, agent := sqliteScanner(t, root)
	folder := filepath.Join(root, "One (2001)")
	if _, _, err := rescanFolder(t.Context(), scan.catalog, scan.folderScan(), folder); err != nil {
		t.Fatal(err)
	}
	if held := agent.rowsFor(t, "movies", "house/movies"); held != 1 {
		t.Fatalf("movies holds %d rows, want the one the first rescan wrote", held)
	}

	// The share refuses the directory that holds the title, so every stat
	// of the title's own folder fails.
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
	_, removed, err := rescanFolder(t.Context(), scan.catalog, scan.folderScan(), folder)

	if err == nil {
		t.Error("the rescan reported no error for a folder it could not read")
	}
	if removed != 0 {
		t.Errorf("the rescan removed %d rows, want none", removed)
	}
	if held := agent.rowsFor(t, "movies", "house/movies"); held != 1 {
		t.Errorf("movies holds %d rows, want the title the volume still has", held)
	}
}

// A credit the folder no longer names leaves the catalog on a rescan of that
// folder, the way a genre does. A folder's credits key on its title's own id,
// so a scoped sweep reaches them through the title row.
func TestARescanSweepsACreditTheFolderNoLongerNames(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	credited := func(billing int) *walkResult {
		walk := walkOfOneTitle("house/movies", "movie:tmdb:1", "One (2001)", "movie:path:one-2001")
		for i := range billing {
			walk.credits = append(walk.credits, creditRow{
				Library: "house/movies", Item: "movie:tmdb:1", Billing: i,
				Contributor: ".contributors/person", Name: "A Person", Part: creditPartActor,
			})
		}
		return walk
	}
	if err := flushWalk(ctx, catalog, credited(3), time.Now().Add(-time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}
	if held := agent.rowsFor(t, "credits", "house/movies"); held != 3 {
		t.Fatalf("credits holds %d rows, want the three the folder named", held)
	}

	epoch := time.Now().UnixNano()
	if err := flushWalk(ctx, catalog, credited(1), epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneScope(ctx, catalog, "house/movies", "One (2001)", epoch); err != nil {
		t.Fatal(err)
	}

	if held := agent.rowsFor(t, "credits", "house/movies"); held != 1 {
		t.Errorf("credits holds %d rows, want the one the folder names now", held)
	}
}

// A run that names no commit keeps the one the last run left, so a scan that
// could not read the mark, and a scan that failed, both leave it where it is.
// A run that names one writes it, which is how a scan that read a new commit
// moves the mark.
func TestARunThatNamesNoCommitKeepsTheOneTheLastRunLeft(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.UpsertRun(ctx, "house/franchises", libraryRun{
		Worker: workerScan, Job: "franchises-scan-1", Commit: "abc123",
	}); err != nil {
		t.Fatal(err)
	}

	if err := catalog.UpsertRun(ctx, "house/franchises", libraryRun{
		Worker: workerScan, Job: "franchises-scan-2", Failure: "the forge answered nothing",
	}); err != nil {
		t.Fatal(err)
	}
	kept, err := catalog.lastScan(ctx, "house/franchises")
	if err != nil {
		t.Fatal(err)
	}

	if kept.Commit != "abc123" {
		t.Errorf("commit = %q, want the mark the last good run left", kept.Commit)
	}
	if kept.Failure != "the forge answered nothing" {
		t.Errorf("failure = %q, want the one the failed run wrote", kept.Failure)
	}

	if err := catalog.UpsertRun(ctx, "house/franchises", libraryRun{
		Worker: workerScan, Job: "franchises-scan-3", Commit: "def456",
	}); err != nil {
		t.Fatal(err)
	}
	moved, err := catalog.lastScan(ctx, "house/franchises")
	if err != nil {
		t.Fatal(err)
	}
	if moved.Commit != "def456" || moved.Failure != "" {
		t.Errorf("the run reads %+v, want the new commit and no failure", moved)
	}
}
