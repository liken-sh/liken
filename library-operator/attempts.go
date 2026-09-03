package main

// attempts.go is the attempts table's Go side: the row, the writes, and how
// the scanner lifts a folder's .liken files into the rows the gap queries
// read. The rows are derived from the volume, so a lost catalog gets them
// back on the next walk.

import (
	"context"
	"path/filepath"
	"strings"
	"time"
)

// The column of the attempts table that holds the fact. Corrosion applies the
// difference between the schema file and the database on every start. It adds
// tables, columns, and indexes, and it refuses to remove a column. This
// column is also part of the primary key, and Corrosion refuses to add a
// primary key column to a table that exists. So the column keeps the name it
// was created with, every Go name is fact, and this constant is the one place
// the two meet.
const attemptFactColumn = "concern"

// One enricher's last attempt at one item, as the attempts table holds it.
// For a file fact the item is the file's path under the library root.
type attemptRow struct {
	Library string
	Item    string
	Fact    string
	At      int64
	Result  string
	// The provider block that answered, empty for a fact that asks no provider.
	// A set fact joins the blocks it took the union of with commas.
	Provider string
}

// One attempts row, by the two key columns that follow the library.
type attemptKey struct {
	Item string
	Fact string
}

// A repeat write updates the row in place, because one item and one fact
// hold one attempt, the latest. The update names no key column, so the row's
// identity never moves.
func (c *Catalog) UpsertAttempts(ctx context.Context, rows []attemptRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		statements[i] = statement{
			sql: `INSERT INTO attempts (library, item, ` + attemptFactColumn + `, at, result, provider) VALUES (?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, item, ` + attemptFactColumn + `) DO UPDATE SET at = excluded.at, ` +
				`result = excluded.result, provider = excluded.provider`,
			params: []any{row.Library, row.Item, row.Fact, row.At, row.Result, row.Provider},
		}
	}
	return c.apply(ctx, statements)
}

// The delete names all three key columns, as the link table's delete does, so
// a sweep takes exactly the rows it marked.
func (c *Catalog) DeleteAttempts(ctx context.Context, library string, keys []attemptKey) (int, error) {
	statements := make([]statement, len(keys))
	for i, key := range keys {
		statements[i] = statement{
			sql:    `DELETE FROM attempts WHERE library = ? AND item = ? AND ` + attemptFactColumn + ` = ?`,
			params: []any{library, key.Item, key.Fact},
		}
	}
	return c.apply(ctx, statements)
}

// The two key columns travel as one string through a sweep, joined by a
// separator no path or id holds, so the sweep's mark table keeps one column
// for every table it covers.
func attemptKeys(keys []string) []attemptKey {
	out := make([]attemptKey, len(keys))
	for i, key := range keys {
		item, fact, _ := strings.Cut(key, linkKeySeparator)
		out[i] = attemptKey{Item: item, Fact: fact}
	}
	return out
}

func attemptSeenKey(row attemptRow) string {
	return row.Item + linkKeySeparator + row.Fact
}

// Reads the attempts this library holds that the current epoch did not mark,
// one bounded batch, with the two key columns joined the way the mark joined
// them.
func attemptPruneSQL() string {
	return `SELECT item || char(31) || ` + attemptFactColumn + ` FROM attempts` +
		` WHERE library = ?` +
		` AND '` + seenAttempt + `' || item || char(31) || ` + attemptFactColumn +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` AND at < ?` +
		` LIMIT ?`
}

// How a rescan reaches one folder's attempts: a file fact keys on a path
// under the folder, and an item fact keys on the id of an item the folder
// holds.
func scopedAttemptPruneSQL() string {
	scope := func(table string) string {
		return `SELECT id FROM ` + table + ` WHERE library = ? AND ` + pathScopeClause("path")
	}
	return `SELECT item || char(31) || ` + attemptFactColumn + ` FROM attempts` +
		` WHERE library = ?` +
		` AND '` + seenAttempt + `' || item || char(31) || ` + attemptFactColumn +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` AND (` + pathScopeClause("item") +
		` OR item IN (` + scope("movies") + ` UNION ` + scope("series") + ` UNION ` + scope("episodes") + `))` +
		` AND at < ?` +
		` LIMIT ?`
}

func scopedAttemptPruneParams(library, folder string, epoch int64) []any {
	params := []any{library, epoch}
	params = append(params, pathScopeParams(folder)...)
	for range 3 {
		params = append(params, library)
		params = append(params, pathScopeParams(folder)...)
	}
	return append(params, walkStart(epoch), pruneBatch)
}

// What one folder's .liken directory means to the scanner: which item the
// folder's own entry names, and which item each file under it names.
type likenSidecar struct {
	root    string
	dir     string
	library string
	item    string
	items   map[string]string
	// The facts whose ledgers this folder can hold. A title folder holds the
	// title's own, which is the list below, and a person's directory holds the
	// three contributor facts and no other.
	facts []string
}

// The facts this folder is read for, which is the title list where the caller
// names none.
func (s likenSidecar) ledgerFacts() []string {
	if s.facts != nil {
		return s.facts
	}
	return likenFacts
}

// The facts the scanner lifts out of a folder. A file fact keys on a
// path, because it works per file, and the identity fact keys on an item
// id, because it works per title.
var likenFacts = []string{factProbe, factTrickplay, factIdentity,
	factOverview, factCertification,
	factRatingTMDb, factRatingIMDb, factRatingRottenTomatoes, factRatingMetacritic,
	factCredits,
	factPoster, factBackdrop, factLogo, factClearart, factBanner,
	factLandscape, factDiscart, factSeasonPoster, factSeasonBanner, factEpisodeThumb}

// Reads every .liken file the folder holds into attempts rows. A folder that
// holds none reads as no rows and not as an error, because most folders hold
// none.
// One pass answers for both kinds of row, because the credits ledger the
// credits fact wrote is one of the files this pass already opens.
func (s likenSidecar) read() ([]attemptRow, []creditRow, error) {
	var rows []attemptRow
	var credits []creditRow
	for _, fact := range s.ledgerFacts() {
		ledger, err := readLikenLedger(s.dir, fact)
		if err != nil {
			return rows, credits, err
		}
		if fact == factCredits {
			credits = append(credits, creditRows(s.library, s.item, ledger.Credits)...)
		}
		for _, attempt := range ledger.Attempts {
			item := s.itemOf(fact, attempt.Path)
			if item == "" || attempt.Result == "" {
				continue
			}
			rows = append(rows, attemptRow{
				Library:  s.library,
				Item:     item,
				Fact:     fact,
				At:       attempt.At.Unix(),
				Result:   attempt.Result,
				Provider: strings.Join(attempt.Provider, ","),
			})
		}
	}
	return rows, credits, nil
}

// How an entry's path resolves: a file fact names the file itself, and an
// item fact names the title the folder holds.
func (s likenSidecar) itemOf(fact, path string) string {
	if _, art := artTypes[fact]; fact == factProbe || fact == factTrickplay || art {
		return relativePath(s.root, filepath.Join(s.dir, path))
	}
	if path == likenSelfPath || path == "" {
		return s.item
	}
	return s.items[path]
}

// A folder whose .liken files cannot be read marks the pass incomplete, the
// way an unreadable sidecar does, so the sweep never removes rows the volume
// still holds.
func readLikenSidecar(sidecar likenSidecar, result *walkResult) {
	rows, credits, err := sidecar.read()
	result.noteReadError(err)
	result.attempts = append(result.attempts, rows...)
	result.credits = append(result.credits, credits...)
}

// The reporter counts a gap with the same query the container works from, so
// the number the operator schedules on and the rows the container finds are
// one set.
func (c *Catalog) gapCounts(ctx context.Context, library string, now time.Time) (map[string]int, error) {
	counts := map[string]int{}
	for fact, query := range gapQueries {
		count, err := c.queryInt(ctx, `SELECT count(*) FROM (`+query+`)`, gapParams(library, now))
		if err != nil {
			return nil, err
		}
		counts[fact] = count
	}
	return counts, nil
}

// The fights of one library: the attempts that found an element group another
// writer had changed, over every fact. A person reads it on the Library, and
// the repair is to stop the other writer.
func (c *Catalog) fightCount(ctx context.Context, library string) (int, error) {
	return c.queryInt(ctx, fightsQuery, []any{library})
}

// The two counts a person reads on the Library beside the gaps: the titles
// that wait for a person, and the titles no provider could name.
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
