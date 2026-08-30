package main

// prune.go reconciles the catalog against the volume by marking and
// sweeping, in place of the in-memory record of the last walk it replaced.
// A full walk marks every id it reads with the walk's epoch in the seen
// table, and the prune deletes the catalog rows the walk did not mark this
// epoch. So a removal survives a restart, and the scanner never holds the
// whole key set in memory.
//
// The seen table is local to the agent and never gossips. A mark on a
// replicated row would gossip to every reader on every walk, and a new
// column on a populated cr-sqlite table backfills a clock row for every
// existing row. The scanner creates seen at runtime, not in the schema
// file, because cr-sqlite makes every table a schema file names a
// replicated table. A table created through the write API stays local.

import "context"

// pruneBatch bounds how many unmarked ids the prune reads and deletes at
// once, so the prune holds one batch and never the whole set. It is a var
// so a test drives several batches over a small set.
var pruneBatch = 500

// pruneMinFraction is the share of the catalog's items a walk must find
// before the prune runs. A walk that finds a far smaller share than the
// catalog holds read only part of the volume, so its prune is skipped and
// the rows stand for the next clean walk. It is a var so a test drives the
// threshold.
var pruneMinFraction = 0.5

// pruneRatioFloor is the item count below which the fraction guard does
// not apply, so a small catalog is not held hostage to a noisy ratio. The
// read-error guard still applies at any size.
var pruneRatioFloor = 8

// ensureSeen creates the local seen table if it does not exist. The table
// is not in the schema file, because every table the schema file names
// becomes a replicated table that gossips. This one is created through the
// write API instead, so it stays a plain local table the agent never
// replicates.
func (c *Catalog) ensureSeen(ctx context.Context) error {
	_, err := c.apply(ctx, []statement{{
		sql: `CREATE TABLE IF NOT EXISTS seen (id TEXT NOT NULL PRIMARY KEY, epoch INTEGER NOT NULL DEFAULT 0)`,
	}})
	return err
}

// markSeen marks every id with the current epoch. A re-mark of an id
// already present updates its epoch in place.
func (c *Catalog) markSeen(ctx context.Context, ids []string, epoch int64) (int, error) {
	statements := make([]statement, len(ids))
	for i, id := range ids {
		statements[i] = statement{
			sql:    `INSERT INTO seen (id, epoch) VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET epoch = excluded.epoch`,
			params: []any{id, epoch},
		}
	}
	return c.apply(ctx, statements)
}

// cleanSeen drops the marks behind the current epoch, so the seen table
// tracks the live catalog and not every id the scanner ever saw. Every row
// the walk kept was marked with the current epoch, so a mark behind it
// belongs to a row the prune removed.
func (c *Catalog) cleanSeen(ctx context.Context, epoch int64) (int, error) {
	return c.apply(ctx, []statement{{
		sql:    `DELETE FROM seen WHERE epoch < ?`,
		params: []any{epoch},
	}})
}

// countItems reads how many item rows the catalog holds for this library,
// across the three item tables. The prune-abort guard reads it to tell a
// complete walk from a walk that returned far fewer rows than the catalog
// holds.
func (c *Catalog) countItems(ctx context.Context, library string) (int, error) {
	return c.queryInt(ctx, `SELECT `+
		`(SELECT count(*) FROM movies WHERE library = ?) + `+
		`(SELECT count(*) FROM series WHERE library = ?) + `+
		`(SELECT count(*) FROM episodes WHERE library = ?)`,
		[]any{library, library, library})
}

// markKeys reads every id, file path, and alias a walk produced into one
// deduplicated list, the set the walk marks with its epoch.
func markKeys(result *walkResult) []string {
	seen := map[string]bool{}
	var keys []string
	add := func(key string) {
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		keys = append(keys, key)
	}
	for _, row := range result.movies {
		add(row.Id)
	}
	for _, row := range result.series {
		add(row.Id)
	}
	for _, row := range result.episodes {
		add(row.Id)
	}
	for _, row := range result.files {
		add(row.Path)
	}
	for _, row := range result.aliases {
		add(row.Alias)
	}
	return keys
}

// incompleteWalk reports whether a walk read only part of the volume, so
// the caller skips the prune and keeps the rows. A read error at the root
// is one signal. A walk that found far fewer items than the catalog holds
// is the other, once the catalog holds more than the ratio floor.
func incompleteWalk(readError bool, items, catalogItems int) bool {
	if readError {
		return true
	}
	if catalogItems > pruneRatioFloor && float64(items) < pruneMinFraction*float64(catalogItems) {
		return true
	}
	return false
}

// sweep reads the unmarked ids one bounded batch at a time and deletes
// each batch, until a query returns fewer than a full batch. It holds one
// batch and never the whole set. It returns the count of rows deleted.
func (c *Catalog) sweep(ctx context.Context, sql string, params []any, del func(ctx context.Context, keys []string) error) (int, error) {
	removed := 0
	for {
		keys, err := c.queryStrings(ctx, sql, params)
		if err != nil {
			return removed, err
		}
		if len(keys) == 0 {
			return removed, nil
		}
		if err := del(ctx, keys); err != nil {
			return removed, err
		}
		removed += len(keys)
		if len(keys) < pruneBatch {
			return removed, nil
		}
	}
}

// pruneLibrary deletes every catalog row this library holds that the
// current epoch did not mark. It reads the unmarked ids through the query
// API and deletes them by id, the form that needs no delete-time join
// against the local seen table. It prunes aliases first, while their items
// still resolve the library scope, then the items, then the files. It
// returns the count of rows removed.
func pruneLibrary(ctx context.Context, catalog *Catalog, library string, epoch int64) (int, error) {
	removed := 0

	n, err := catalog.sweep(ctx, aliasPruneSQL(), aliasPruneParams(library, epoch),
		func(ctx context.Context, keys []string) error {
			_, err := catalog.DeleteAliases(ctx, keys)
			return err
		})
	if err != nil {
		return removed, err
	}
	removed += n

	for _, table := range []struct {
		name   string
		delete func(context.Context, []string) (int, error)
	}{
		{"movies", catalog.DeleteMovies},
		{"series", catalog.DeleteSeries},
		{"episodes", catalog.DeleteEpisodes},
	} {
		n, err := catalog.sweep(ctx, itemPruneSQL(table.name, "id"), []any{library, epoch, pruneBatch},
			func(ctx context.Context, keys []string) error {
				_, err := table.delete(ctx, keys)
				return err
			})
		if err != nil {
			return removed, err
		}
		removed += n
	}

	n, err = catalog.sweep(ctx, itemPruneSQL("files", "path"), []any{library, epoch, pruneBatch},
		func(ctx context.Context, keys []string) error {
			if _, err := catalog.DeleteFiles(ctx, keys); err != nil {
				return err
			}
			_, err := catalog.DeleteFileItemsByPath(ctx, keys)
			return err
		})
	if err != nil {
		return removed, err
	}
	removed += n

	if _, err := catalog.cleanSeen(ctx, epoch); err != nil {
		return removed, err
	}
	return removed, nil
}

// itemPruneSQL reads the keys of a table this library holds that the
// current epoch did not mark, one bounded batch. table and key are
// constants this package names and never input.
func itemPruneSQL(table, key string) string {
	return `SELECT ` + key + ` FROM ` + table +
		` WHERE library = ? AND ` + key +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?) LIMIT ?`
}

// aliasPruneSQL reads the aliases this library's items hold that the
// current epoch did not mark. An alias carries no library, so the scope is
// the set of item ids the three item tables hold for the library.
func aliasPruneSQL() string {
	return `SELECT alias FROM aliases` +
		` WHERE alias NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` AND item IN (` +
		`SELECT id FROM movies WHERE library = ?` +
		` UNION SELECT id FROM series WHERE library = ?` +
		` UNION SELECT id FROM episodes WHERE library = ?)` +
		` LIMIT ?`
}

func aliasPruneParams(library string, epoch int64) []any {
	return []any{epoch, library, library, library, pruneBatch}
}

// pathScopeClause matches the rows of one title folder: the item at the
// folder's own path, and every file and episode under it. It uses a range
// over the path rather than a LIKE, so a folder name that holds a LIKE
// metacharacter still scopes correctly and needs no escape.
const pathScopeClause = `(path = ? OR (path >= ? AND path < ?))`

// pathScopeParams renders the three bounds pathScopeClause reads: the
// folder's own path, and the half-open range that holds every path under
// it. The upper bound is the folder path with the byte after the
// separator, so it stops at the end of the folder's children.
func pathScopeParams(folder string) []any {
	return []any{folder, folder + "/", folder + "0"}
}

// pruneScope deletes the rows of one title folder that the current epoch
// did not mark, the reconciliation a webhook rescan drives. A folder still
// on the volume keeps the rows the rescan re-read; a folder that left the
// volume marks nothing, so every one of its rows is unmarked and leaves.
// It reads no seen marks behind the epoch, because a full walk owns that
// cleanup. It returns the count of rows removed.
func pruneScope(ctx context.Context, catalog *Catalog, library, folder string, epoch int64) (int, error) {
	removed := 0

	n, err := catalog.sweep(ctx, scopedAliasPruneSQL(), scopedAliasPruneParams(library, folder, epoch),
		func(ctx context.Context, keys []string) error {
			_, err := catalog.DeleteAliases(ctx, keys)
			return err
		})
	if err != nil {
		return removed, err
	}
	removed += n

	for _, table := range []struct {
		name   string
		delete func(context.Context, []string) (int, error)
	}{
		{"movies", catalog.DeleteMovies},
		{"series", catalog.DeleteSeries},
		{"episodes", catalog.DeleteEpisodes},
	} {
		n, err := catalog.sweep(ctx, scopedItemPruneSQL(table.name, "id"), scopedItemPruneParams(library, folder, epoch),
			func(ctx context.Context, keys []string) error {
				_, err := table.delete(ctx, keys)
				return err
			})
		if err != nil {
			return removed, err
		}
		removed += n
	}

	n, err = catalog.sweep(ctx, scopedItemPruneSQL("files", "path"), scopedItemPruneParams(library, folder, epoch),
		func(ctx context.Context, keys []string) error {
			if _, err := catalog.DeleteFiles(ctx, keys); err != nil {
				return err
			}
			_, err := catalog.DeleteFileItemsByPath(ctx, keys)
			return err
		})
	if err != nil {
		return removed, err
	}
	removed += n
	return removed, nil
}

// scopedItemPruneSQL reads the keys of one folder's rows in a table that
// the current epoch did not mark, one bounded batch.
func scopedItemPruneSQL(table, key string) string {
	return `SELECT ` + key + ` FROM ` + table +
		` WHERE library = ? AND ` + pathScopeClause + ` AND ` + key +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?) LIMIT ?`
}

func scopedItemPruneParams(library, folder string, epoch int64) []any {
	params := []any{library}
	params = append(params, pathScopeParams(folder)...)
	return append(params, epoch, pruneBatch)
}

// scopedAliasPruneSQL reads the aliases of one folder's items that the
// current epoch did not mark, scoped by the item ids the folder holds.
func scopedAliasPruneSQL() string {
	scope := func(table string) string {
		return `SELECT id FROM ` + table + ` WHERE library = ? AND ` + pathScopeClause
	}
	return `SELECT alias FROM aliases` +
		` WHERE alias NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` AND item IN (` +
		scope("movies") + ` UNION ` + scope("series") + ` UNION ` + scope("episodes") +
		`) LIMIT ?`
}

func scopedAliasPruneParams(library, folder string, epoch int64) []any {
	params := []any{epoch}
	for range 3 {
		params = append(params, library)
		params = append(params, pathScopeParams(folder)...)
	}
	return append(params, pruneBatch)
}
