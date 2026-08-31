package main

// fakeCatalog is a stateful stand-in for a Corrosion agent: it holds the
// catalog tables in memory and answers both the write API and the query
// API, so a test drives a whole mark-and-sweep against a real client with
// no agent. It interprets only the statements the scanner sends, which is
// the whole set the reconciliation uses.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeRow is one catalog row, holding the two columns the prune reads:
// the library the row belongs to, and its path. Every table the fake
// holds is keyed the way the schema keys it, by the library and the
// row's own key joined with a NUL, so the same id under two libraries
// is two rows here, as it is in the database.
type fakeRow struct {
	library string
	path    string
}

// fakeKey renders that composite map key from its parts.
func fakeKey(parts ...string) string {
	return strings.Join(parts, "\x00")
}

type fakeCatalog struct {
	mu         sync.Mutex
	movies     map[string]fakeRow
	series     map[string]fakeRow
	episodes   map[string]fakeRow
	files      map[string]fakeRow
	aliases    map[string]string
	fileItems  map[string]bool
	seen       map[string]int64
	statements []capturedStatement
	failStatus int
	// seenLagReads models a real Corrosion agent right after CREATE
	// TABLE seen: a query of seen can still miss the table for a short
	// window. Each read of seen decrements it and answers "no such table"
	// until it reaches zero.
	seenLagReads int
	// countErrorsAfter fails the item count read once this many have
	// been served, so a test drives the second count read of a walk failing
	// while the first succeeds. Zero serves every count read.
	countErrorsAfter int
	countReadsServed int
	// movieBatches counts the transaction POSTs that carried a movie upsert,
	// so a test reads how many times the streaming full walk flushed.
	movieBatches int
	// keepDeleted answers every delete with no rows affected and keeps
	// the row, which models an agent whose delete lands on no row while
	// the query still reads it. A test drives the sweep's no-progress
	// guard with it.
	keepDeleted bool
}

// newFakeCatalog builds an empty catalog and the client that writes and
// reads it, and ends the server with the test.
func newFakeCatalog(t *testing.T) (*Catalog, *fakeCatalog) {
	t.Helper()
	fake := &fakeCatalog{
		movies:    map[string]fakeRow{},
		series:    map[string]fakeRow{},
		episodes:  map[string]fakeRow{},
		files:     map[string]fakeRow{},
		aliases:   map[string]string{},
		fileItems: map[string]bool{},
		seen:      map[string]int64{},
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return NewCatalog(server.URL, server.Client()), fake
}

func (f *fakeCatalog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failStatus != 0 {
		w.WriteHeader(f.failStatus)
		return
	}
	if strings.HasSuffix(r.URL.Path, queriesPath) {
		f.serveQuery(w, r)
		return
	}
	f.serveTransactions(w, r)
}

func (f *fakeCatalog) serveTransactions(w http.ResponseWriter, r *http.Request) {
	statements := parseStatements(readBody(r))
	f.statements = append(f.statements, statements...)
	for _, s := range statements {
		if strings.HasPrefix(s.sql, "INSERT INTO movies") {
			f.movieBatches++
			break
		}
	}
	results := make([]map[string]any, len(statements))
	for i, s := range statements {
		if f.keepDeleted && strings.HasPrefix(s.sql, "DELETE") {
			results[i] = map[string]any{"rows_affected": 0}
			continue
		}
		f.apply(s)
		results[i] = map[string]any{"rows_affected": 1}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
}

// apply mutates the in-memory tables the way one write statement would.
func (f *fakeCatalog) apply(s capturedStatement) {
	p := s.params
	switch {
	case strings.HasPrefix(s.sql, "INSERT INTO movies"):
		f.movies[fakeKey(str(p[0]), str(p[1]))] = fakeRow{str(p[0]), str(p[3])}
	case strings.HasPrefix(s.sql, "INSERT INTO series"):
		f.series[fakeKey(str(p[0]), str(p[1]))] = fakeRow{str(p[0]), str(p[3])}
	case strings.HasPrefix(s.sql, "INSERT INTO episodes"):
		f.episodes[fakeKey(str(p[0]), str(p[1]))] = fakeRow{str(p[0]), str(p[3])}
	case strings.HasPrefix(s.sql, "INSERT INTO files"):
		f.files[fakeKey(str(p[0]), str(p[1]))] = fakeRow{str(p[0]), str(p[1])}
	case strings.HasPrefix(s.sql, "INSERT INTO file_items"):
		f.fileItems[fakeKey(str(p[0]), str(p[1]), str(p[2]))] = true
	case strings.HasPrefix(s.sql, "INSERT INTO aliases"):
		f.aliases[fakeKey(str(p[0]), str(p[1]))] = str(p[2])
	case strings.HasPrefix(s.sql, "INSERT INTO seen"):
		f.seen[str(p[0])] = num(p[1])
	case strings.HasPrefix(s.sql, "DELETE FROM movies"):
		delete(f.movies, fakeKey(str(p[0]), str(p[1])))
	case strings.HasPrefix(s.sql, "DELETE FROM series"):
		delete(f.series, fakeKey(str(p[0]), str(p[1])))
	case strings.HasPrefix(s.sql, "DELETE FROM episodes"):
		delete(f.episodes, fakeKey(str(p[0]), str(p[1])))
	case strings.HasPrefix(s.sql, "DELETE FROM files"):
		delete(f.files, fakeKey(str(p[0]), str(p[1])))
	case strings.HasPrefix(s.sql, "DELETE FROM aliases"):
		delete(f.aliases, fakeKey(str(p[0]), str(p[1])))
	case strings.HasPrefix(s.sql, "DELETE FROM file_items"):
		delete(f.fileItems, fakeKey(str(p[0]), str(p[1]), str(p[2])))
	case strings.HasPrefix(s.sql, "DELETE FROM seen"):
		f.cleanSeen(num(p[0]))
	}
}

func (f *fakeCatalog) cleanSeen(epoch int64) {
	for id, marked := range f.seen {
		if marked < epoch {
			delete(f.seen, id)
		}
	}
}

// serveQuery answers a read the way the agent streams it: a columns event,
// one row event per result, then the end-of-query marker.
func (f *fakeCatalog) serveQuery(w http.ResponseWriter, r *http.Request) {
	sql, params := parseQuery(readBody(r))
	// while the seen table is not yet visible, a read that names it
	// answers the error a real agent streams, so a test drives the prune
	// read that misses the freshly created seen table.
	if f.seenLagReads > 0 && strings.Contains(sql, "seen") {
		f.seenLagReads--
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "no such table: seen"})
		return
	}
	// after the configured number of count reads, the next one fails,
	// so a test drives the walk's second count read failing. The prune's
	// own read of the seen table is not one of the walk's counts.
	if strings.Contains(sql, "count(*)") && !strings.Contains(sql, "FROM seen") {
		f.countReadsServed++
		if f.countErrorsAfter > 0 && f.countReadsServed > f.countErrorsAfter {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "database is locked"})
			return
		}
	}
	rows := f.evaluate(sql, params)

	enc := json.NewEncoder(w)
	_ = enc.Encode(map[string]any{"columns": []string{"c"}})
	for i, value := range rows {
		_ = enc.Encode(map[string]any{"row": []any{i + 1, []any{value}}})
	}
	_ = enc.Encode(map[string]any{"eoq": map[string]any{"time": 0.0}})
}

// evaluate runs the one read the SQL asks for and returns its cells.
func (f *fakeCatalog) evaluate(sql string, p []any) []any {
	switch {
	case strings.Contains(sql, "count(*) FROM seen"):
		return []any{float64(f.countMarks(num(p[0])))}
	case strings.Contains(sql, "count(*) FROM files"):
		return []any{float64(f.countIn(f.files, str(p[0])))}
	case strings.Contains(sql, "count(*)"):
		lib := str(p[0])
		count := f.countIn(f.movies, lib) + f.countIn(f.series, lib) + f.countIn(f.episodes, lib)
		return []any{float64(count)}
	case strings.Contains(sql, "FROM file_items"):
		return f.unmarkedLinks(sql, p)
	case strings.Contains(sql, "FROM aliases"):
		return f.unmarkedAliases(sql, p)
	default:
		return f.unmarkedItems(sql, p)
	}
}

// unmarkedLinks reads the links this library holds that the current
// epoch did not mark, honoring the folder scope where the query names
// one. A link row carries its own library, so the read reaches no
// other table.
func (f *fakeCatalog) unmarkedLinks(sql string, p []any) []any {
	scoped := strings.Contains(sql, "path >=")
	var library, folder string
	var epoch int64
	if scoped {
		library, folder, epoch = str(p[0]), str(p[1]), num(p[4])
	} else {
		library, epoch = str(p[0]), num(p[1])
	}
	var keys []any
	for key := range f.fileItems {
		rowLibrary, rest, _ := strings.Cut(key, "\x00")
		path, item, _ := strings.Cut(rest, "\x00")
		if rowLibrary != library {
			continue
		}
		if scoped && !inScope(path, folder) {
			continue
		}
		if f.seen[seenPrefix(sql)+path+linkKeySeparator+item] == epoch {
			continue
		}
		keys = append(keys, path+linkKeySeparator+item)
	}
	return keys
}

// countMarks reports how many ids this epoch marked, the read the prune
// guard makes before it sweeps anything.
func (f *fakeCatalog) countMarks(epoch int64) int {
	count := 0
	for _, marked := range f.seen {
		if marked == epoch {
			count++
		}
	}
	return count
}

func (f *fakeCatalog) countIn(table map[string]fakeRow, library string) int {
	count := 0
	for _, row := range table {
		if row.library == library {
			count++
		}
	}
	return count
}

// unmarkedItems reads the keys of one item or file table the current epoch
// did not mark, honoring the library scope and the folder scope where the
// query names one.
func (f *fakeCatalog) unmarkedItems(sql string, p []any) []any {
	table := f.tableOf(sql)
	scoped := strings.Contains(sql, "path >=")
	var library, folder string
	var epoch int64
	if scoped {
		library, folder, epoch = str(p[0]), str(p[1]), num(p[4])
	} else {
		library, epoch = str(p[0]), num(p[1])
	}
	var keys []any
	for composite, row := range table {
		_, key, _ := strings.Cut(composite, "\x00")
		if row.library != library {
			continue
		}
		if scoped && !inScope(row.path, folder) {
			continue
		}
		if f.seen[seenPrefix(sql)+key] == epoch {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

// unmarkedAliases reads this library's aliases that the current epoch
// did not mark. The alias row carries the library, so the library
// scopes it the way it scopes an item table, and the item is read only
// to narrow a scoped prune to one folder.
func (f *fakeCatalog) unmarkedAliases(sql string, p []any) []any {
	scoped := strings.Contains(sql, "path >=")
	library, epoch := str(p[0]), num(p[1])
	folder := ""
	if scoped {
		folder = str(p[3])
	}
	scope := f.scopeItems(library, folder, scoped)
	var keys []any
	for composite, item := range f.aliases {
		rowLibrary, alias, _ := strings.Cut(composite, "\x00")
		if rowLibrary != library {
			continue
		}
		if f.seen[seenPrefix(sql)+alias] == epoch {
			continue
		}
		if !scoped || scope[item] {
			keys = append(keys, alias)
		}
	}
	return keys
}

// scopeItems is the set of item ids the alias prune resolves to: the
// library's items, narrowed to one folder where the prune is scoped.
func (f *fakeCatalog) scopeItems(library, folder string, scoped bool) map[string]bool {
	scope := map[string]bool{}
	for _, table := range []map[string]fakeRow{f.movies, f.series, f.episodes} {
		for composite, row := range table {
			_, id, _ := strings.Cut(composite, "\x00")
			if row.library != library {
				continue
			}
			if scoped && !inScope(row.path, folder) {
				continue
			}
			scope[id] = true
		}
	}
	return scope
}

func (f *fakeCatalog) tableOf(sql string) map[string]fakeRow {
	switch {
	case strings.Contains(sql, "FROM movies"):
		return f.movies
	case strings.Contains(sql, "FROM series"):
		return f.series
	case strings.Contains(sql, "FROM episodes"):
		return f.episodes
	default:
		return f.files
	}
}

// held reads a whole table under the lock, so a test reads no torn map
// while a request is in flight.
// heldLinks copies the file-to-item links, whose keys are the library,
// the path, and the item joined by NULs, so a test reads them without
// the lock.
func (f *fakeCatalog) heldLinks() map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]bool{}
	for k := range f.fileItems {
		out[k] = true
	}
	return out
}

func (f *fakeCatalog) held(table map[string]fakeRow) map[string]fakeRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]fakeRow{}
	for k, v := range table {
		out[k] = v
	}
	return out
}

// counts reports how many count queries the fake has served.
func (f *fakeCatalog) counts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.countReadsServed
}

// failCountsAfter fails every count query after the one numbered here,
// so a test drives a walk whose counts succeed and whose later reads fail.
func (f *fakeCatalog) failCountsAfter(served int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.countErrorsAfter = served
}

// flushes reads how many transaction POSTs carried a movie upsert under the
// lock, so a test reads the flush count while no request is in flight.
func (f *fakeCatalog) flushes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.movieBatches
}

// deletesNothing makes every later delete land on no row, so a test
// drives a sweep that reads the same batch again and again.
func (f *fakeCatalog) deletesNothing() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keepDeleted = true
}

func inScope(path, folder string) bool {
	return path == folder || strings.HasPrefix(path, folder+"/")
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
}

func readBody(r *http.Request) []byte {
	body, _ := io.ReadAll(r.Body)
	return body
}

// parseQuery decodes a query body, which is a bare SQL string or a
// [sql, [params]] pair.
func parseQuery(body []byte) (string, []any) {
	var simple string
	if json.Unmarshal(body, &simple) == nil {
		return simple, nil
	}
	var pair []json.RawMessage
	if json.Unmarshal(body, &pair) != nil || len(pair) != 2 {
		return "", nil
	}
	var sql string
	_ = json.Unmarshal(pair[0], &sql)
	var params []any
	_ = json.Unmarshal(pair[1], &params)
	return sql, params
}

// seenPrefix reads the key space a prune query concatenates onto its column,
// the first quoted literal in the statement. The fake reads it out of the SQL
// rather than assuming one, so a query that marks the wrong key space fails
// here the way it would against a real agent.
func seenPrefix(sql string) string {
	_, rest, found := strings.Cut(sql, "'")
	if !found {
		return ""
	}
	prefix, _, _ := strings.Cut(rest, "'")
	return prefix
}
