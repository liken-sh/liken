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

// fakeRow is one catalog row the fake keys on, holding the two columns the
// prune reads: the library the row belongs to, and its path.
type fakeRow struct {
	library string
	path    string
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
	for _, s := range statements {
		f.apply(s)
	}
	results := make([]map[string]any, len(statements))
	for i := range results {
		results[i] = map[string]any{"rows_affected": 1}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
}

// apply mutates the in-memory tables the way one write statement would.
func (f *fakeCatalog) apply(s capturedStatement) {
	p := s.params
	switch {
	case strings.HasPrefix(s.sql, "INSERT INTO movies"):
		f.movies[str(p[0])] = fakeRow{str(p[1]), str(p[3])}
	case strings.HasPrefix(s.sql, "INSERT INTO series"):
		f.series[str(p[0])] = fakeRow{str(p[1]), str(p[3])}
	case strings.HasPrefix(s.sql, "INSERT INTO episodes"):
		f.episodes[str(p[0])] = fakeRow{str(p[1]), str(p[3])}
	case strings.HasPrefix(s.sql, "INSERT INTO files"):
		f.files[str(p[0])] = fakeRow{str(p[1]), str(p[0])}
	case strings.HasPrefix(s.sql, "INSERT INTO file_items"):
		f.fileItems[str(p[0])+"\x00"+str(p[1])] = true
	case strings.HasPrefix(s.sql, "INSERT INTO aliases"):
		f.aliases[str(p[0])] = str(p[1])
	case strings.HasPrefix(s.sql, "INSERT INTO seen"):
		f.seen[str(p[0])] = num(p[1])
	case strings.HasPrefix(s.sql, "DELETE FROM movies"):
		delete(f.movies, str(p[0]))
	case strings.HasPrefix(s.sql, "DELETE FROM series"):
		delete(f.series, str(p[0]))
	case strings.HasPrefix(s.sql, "DELETE FROM episodes"):
		delete(f.episodes, str(p[0]))
	case strings.HasPrefix(s.sql, "DELETE FROM files"):
		delete(f.files, str(p[0]))
	case strings.HasPrefix(s.sql, "DELETE FROM aliases"):
		delete(f.aliases, str(p[0]))
	case strings.HasPrefix(s.sql, "DELETE FROM file_items"):
		f.deleteFileItems(str(p[0]))
	case strings.HasPrefix(s.sql, "DELETE FROM seen"):
		f.cleanSeen(num(p[0]))
	}
}

func (f *fakeCatalog) deleteFileItems(path string) {
	for key := range f.fileItems {
		if strings.HasPrefix(key, path+"\x00") {
			delete(f.fileItems, key)
		}
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
	// so a test drives the walk's second count read failing.
	if strings.Contains(sql, "count(*)") {
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
	case strings.Contains(sql, "count(*) FROM files"):
		return []any{float64(f.countIn(f.files, str(p[0])))}
	case strings.Contains(sql, "count(*)"):
		lib := str(p[0])
		count := f.countIn(f.movies, lib) + f.countIn(f.series, lib) + f.countIn(f.episodes, lib)
		return []any{float64(count)}
	case strings.Contains(sql, "FROM aliases"):
		return f.unmarkedAliases(sql, p)
	default:
		return f.unmarkedItems(sql, p)
	}
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
	for key, row := range table {
		if row.library != library {
			continue
		}
		if scoped && !inScope(row.path, folder) {
			continue
		}
		if f.seen[key] == epoch {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

// unmarkedAliases reads the aliases the current epoch did not mark whose
// item the library, and the folder where the query names one, hold.
func (f *fakeCatalog) unmarkedAliases(sql string, p []any) []any {
	scoped := strings.Contains(sql, "path >=")
	epoch := num(p[0])
	var library, folder string
	if scoped {
		library, folder = str(p[1]), str(p[2])
	} else {
		library = str(p[1])
	}
	scope := f.scopeItems(library, folder, scoped)
	var keys []any
	for alias, item := range f.aliases {
		if f.seen[alias] == epoch {
			continue
		}
		if scope[item] {
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
		for id, row := range table {
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
