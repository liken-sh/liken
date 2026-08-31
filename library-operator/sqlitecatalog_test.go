package main

// This file runs the scanner's own SQL against a real SQLite database
// loaded with the shipped schema, so the mark-and-sweep is proved against
// the column names, the key spaces, and the joins the agent holds, and
// not against a fake that reads the statements by keyword. A query that
// names a column the schema does not have fails here.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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
	a.serveTransaction(w, r)
}

// serveTransaction runs each posted statement and answers with one
// result per statement, the shape the write client reads.
func (a *sqliteAgent) serveTransaction(w http.ResponseWriter, r *http.Request) {
	statements := parseStatements(readBody(r))
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

// holdsItem reports whether one item id is still in a table.
func (a *sqliteAgent) holdsItem(t *testing.T, table, id string) bool {
	t.Helper()
	count := 0
	if err := a.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE id = ?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count > 0
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
			{Alias: id, Item: id, Source: aliasSourceProvider},
			{Alias: folderKey, Item: id, Source: aliasSourceFolder},
		},
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

	first := int64(1000)
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
	second := int64(2000)
	if err := flushWalk(ctx, catalog, walkOfOneTitle("house/movies", "movie:tmdb:1", "One (2001)", "movie:path:one-2001"), second); err != nil {
		t.Fatal(err)
	}

	removed, err := pruneLibrary(ctx, catalog, "house/movies", second)
	if err != nil {
		t.Fatal(err)
	}
	// The departed title's item, its file, its link, and its two aliases.
	if removed != 5 {
		t.Errorf("removed = %d, want the departed title's five rows", removed)
	}
	if !agent.holdsItem(t, "movies", "movie:tmdb:1") {
		t.Error("the marked title was swept")
	}
	if agent.holdsItem(t, "movies", "movie:tmdb:2") {
		t.Error("the unmarked title stands")
	}
	if !agent.holdsItem(t, "movies", "movie:tmdb:9") {
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
	if got := agent.rowCount(t, "seen"); got != 5 {
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

	first := int64(1000)
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
	removed, err := pruneScope(ctx, catalog, "house/movies", "100% Wolf (2020)", int64(2000))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 5 {
		t.Errorf("removed = %d, want the folder's five rows", removed)
	}
	if agent.holdsItem(t, "movies", "movie:tmdb:1") {
		t.Error("the departed folder's title stands")
	}
	if !agent.holdsItem(t, "movies", "movie:tmdb:2") {
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
	if err := flushWalk(ctx, catalog, walkOfOneTitle("house/movies", "movie:path:one-2001", "One (2001)", "movie:path:one-2001"), int64(1000)); err != nil {
		t.Fatal(err)
	}

	// The sidecar arrived, so the title's id is now the provider id and
	// the old id is one of its aliases.
	second := int64(2000)
	if err := flushWalk(ctx, catalog, walkOfOneTitle("house/movies", "movie:tmdb:1", "One (2001)", "movie:path:one-2001"), second); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneLibrary(ctx, catalog, "house/movies", second); err != nil {
		t.Fatal(err)
	}

	if agent.holdsItem(t, "movies", "movie:path:one-2001") {
		t.Error("the stale path-derived item stands, so the catalog holds the title twice")
	}
	if !agent.holdsItem(t, "movies", "movie:tmdb:1") {
		t.Error("the provider-derived item was swept")
	}
	if got := agent.rowCount(t, "aliases"); got != 2 {
		t.Errorf("aliases = %d, want the provider id and the folder key", got)
	}
}
