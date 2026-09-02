package main

// PROSE: this file is the attempts table's Go side: the row, the writes, and
// how the scanner lifts a folder's .liken files into rows the gap queries read.

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// PROSE: one enricher's last attempt at one item, as the attempts table holds
// it. For a file concern the item is the file's path under the library root.
type attemptRow struct {
	Library string
	Item    string
	Concern string
	At      int64
	Result  string
}

// PROSE: names one attempts row by the two columns that follow the library.
type attemptKey struct {
	Item    string
	Concern string
}

// PROSE: says why a repeat write updates the row in place, and why the update
// names no key column.
func (c *Catalog) UpsertAttempts(ctx context.Context, rows []attemptRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		statements[i] = statement{
			sql: `INSERT INTO attempts (library, item, concern, at, result) VALUES (?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, item, concern) DO UPDATE SET at = excluded.at, result = excluded.result`,
			params: []any{row.Library, row.Item, row.Concern, row.At, row.Result},
		}
	}
	return c.apply(ctx, statements)
}

// PROSE: says why the delete names all three key columns, as the link table's
// delete does.
func (c *Catalog) DeleteAttempts(ctx context.Context, library string, keys []attemptKey) (int, error) {
	statements := make([]statement, len(keys))
	for i, key := range keys {
		statements[i] = statement{
			sql:    `DELETE FROM attempts WHERE library = ? AND item = ? AND concern = ?`,
			params: []any{library, key.Item, key.Concern},
		}
	}
	return c.apply(ctx, statements)
}

// PROSE: says why the two key columns travel as one string through a sweep,
// joined by the separator no path or id holds.
func attemptKeys(keys []string) []attemptKey {
	out := make([]attemptKey, len(keys))
	for i, key := range keys {
		item, concern, _ := strings.Cut(key, linkKeySeparator)
		out[i] = attemptKey{Item: item, Concern: concern}
	}
	return out
}

func attemptSeenKey(row attemptRow) string {
	return row.Item + linkKeySeparator + row.Concern
}

// PROSE: reads the attempts this library holds that the current epoch did not
// mark, one bounded batch, with the two key columns joined the way the mark
// joined them.
func attemptPruneSQL() string {
	return `SELECT item || char(31) || concern FROM attempts` +
		` WHERE library = ?` +
		` AND '` + seenAttempt + `' || item || char(31) || concern` +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` LIMIT ?`
}

// PROSE: says how a rescan reaches one folder's attempts: a file concern keys
// on a path under the folder, and an item concern keys on the id of an item
// the folder holds.
func scopedAttemptPruneSQL() string {
	scope := func(table string) string {
		return `SELECT id FROM ` + table + ` WHERE library = ? AND ` + pathScopeClause("path")
	}
	return `SELECT item || char(31) || concern FROM attempts` +
		` WHERE library = ?` +
		` AND '` + seenAttempt + `' || item || char(31) || concern` +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` AND (` + pathScopeClause("item") +
		` OR item IN (` + scope("movies") + ` UNION ` + scope("series") + ` UNION ` + scope("episodes") + `))` +
		` LIMIT ?`
}

func scopedAttemptPruneParams(library, folder string, epoch int64) []any {
	params := []any{library, epoch}
	params = append(params, pathScopeParams(folder)...)
	for range 3 {
		params = append(params, library)
		params = append(params, pathScopeParams(folder)...)
	}
	return append(params, pruneBatch)
}

// PROSE: what one folder's .liken directory means to the scanner: which item
// the folder's own entry names, and which item each file under it names.
type likenSidecar struct {
	root    string
	dir     string
	library string
	item    string
	items   map[string]string
}

// PROSE: the concerns the scanner lifts out of a folder, and why the file
// concern keys on a path while the identity concern keys on an item id.
var likenConcerns = []string{concernProbe, concernIdentity}

// PROSE: reads every .liken file the folder holds into attempts rows, and says
// why a folder that holds none reads as no rows rather than an error.
func (s likenSidecar) attempts() ([]attemptRow, error) {
	var rows []attemptRow
	for _, concern := range likenConcerns {
		ledger, err := readLikenLedger(s.dir, concern)
		if err != nil {
			return rows, err
		}
		for _, attempt := range ledger.Attempts {
			item := s.itemOf(concern, attempt.Path)
			if item == "" || attempt.Result == "" {
				continue
			}
			rows = append(rows, attemptRow{
				Library: s.library,
				Item:    item,
				Concern: concern,
				At:      attempt.At.Unix(),
				Result:  attempt.Result,
			})
		}
	}
	return rows, nil
}

// PROSE: says how an entry's path resolves: a file concern names the file
// itself, and an item concern names the title the folder holds.
func (s likenSidecar) itemOf(concern, path string) string {
	if concern == concernProbe {
		return relativePath(s.root, filepath.Join(s.dir, path))
	}
	if path == likenSelfPath || path == "" {
		return s.item
	}
	return s.items[path]
}

// PROSE: says why a folder whose .liken files cannot be read marks the pass
// incomplete rather than writing rows the volume does not hold.
func readLikenSidecar(sidecar likenSidecar, result *walkResult) {
	rows, err := sidecar.attempts()
	result.noteReadError(err)
	result.attempts = append(result.attempts, rows...)
}

// PROSE: says why the season folders are read in name order, so a re-walk
// writes the same rows in the same order.
func sortedDirectories(items map[string]map[string]string) []string {
	dirs := make([]string, 0, len(items))
	for dir := range items {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	return dirs
}

// PROSE: says why the reporter counts a gap with the same query the container
// works from, so the number the operator schedules on and the rows the
// container finds are one set.
func (c *Catalog) gapCounts(ctx context.Context, library string, now time.Time) (map[string]int, error) {
	cutoff := now.Add(-defaultRetryInterval).Unix()
	counts := map[string]int{}
	for concern, query := range gapQueries {
		count, err := c.queryInt(ctx, `SELECT count(*) FROM (`+query+`)`, []any{library, cutoff})
		if err != nil {
			return nil, err
		}
		counts[concern] = count
	}
	return counts, nil
}

// PROSE: the two counts a person reads on the Library beside the gaps: the
// titles that wait for a person, and the titles no provider could name.
func (c *Catalog) identityCounts(ctx context.Context, library string) (int, int, error) {
	waiting, err := c.queryInt(ctx, waitingQuery, []any{library})
	if err != nil {
		return 0, 0, err
	}
	unresolved, err := c.queryInt(ctx, unresolvedQuery, []any{library})
	if err != nil {
		return 0, 0, err
	}
	return waiting, unresolved, nil
}
