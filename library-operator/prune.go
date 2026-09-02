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

import (
	"context"
	"fmt"
	"strings"
)

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
//
// The index on epoch is what every prune query reads, because each one
// asks for the ids this epoch marked.
func (c *Catalog) ensureSeen(ctx context.Context) error {
	_, err := c.apply(ctx, []statement{
		{sql: `CREATE TABLE IF NOT EXISTS seen (id TEXT NOT NULL PRIMARY KEY, epoch INTEGER NOT NULL DEFAULT 0)`},
		{sql: `CREATE INDEX IF NOT EXISTS seen_epoch ON seen (epoch)`},
	})
	return err
}

// The key spaces of the seen table. Four kinds of key are marked, and an
// alias can be the same string as an item's id: a title that gains a
// provider id keeps its old path-derived id as an alias of the new one. With
// one key space, that alias marks the stale item row every walk, and the
// prune never removes it, so the catalog holds the title twice. Each kind of
// key carries its own prefix, so an alias marks only aliases.
const (
	seenItem  = "item:"
	seenFile  = "file:"
	seenAlias = "alias:"
	seenLink  = "link:"
)

// The separator between a link key's two halves. A path and an item id can
// both hold most characters, so the separator is one neither ever holds, and
// no two different pairs render the same key. SQL rebuilds the identical
// string with char(31).
const linkKeySeparator = "\x1f"

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

// countSeen reads how many ids this epoch marked. The prune guard reads
// it, because an epoch that marked nothing would sweep every row the
// library holds.
func (c *Catalog) countSeen(ctx context.Context, epoch int64) (int, error) {
	return c.queryInt(ctx, `SELECT count(*) FROM seen WHERE epoch = ?`, []any{epoch})
}

// countFiles reads how many file rows the catalog holds for this
// library. The report carries it beside the item count, so a Library's
// status shows both.
func (c *Catalog) countFiles(ctx context.Context, library string) (int, error) {
	return c.queryInt(ctx, `SELECT count(*) FROM files WHERE library = ?`, []any{library})
}

// markKeys reads every id, file path, link, and alias a walk produced into one
// deduplicated list, the set the walk marks with its epoch. Each key carries
// the prefix of its own key space, so an alias that reads the same as an
// item's id marks the alias and not the item.
func markKeys(result *walkResult) []string {
	seen := map[string]bool{}
	var keys []string
	add := func(space, key string) {
		if key == "" || seen[space+key] {
			return
		}
		seen[space+key] = true
		keys = append(keys, space+key)
	}
	for _, row := range result.movies {
		add(seenItem, row.Id)
	}
	for _, row := range result.sets {
		add(seenItem, row.Id)
	}
	for _, row := range result.series {
		add(seenItem, row.Id)
	}
	for _, row := range result.episodes {
		add(seenItem, row.Id)
	}
	for _, row := range result.files {
		add(seenFile, row.Path)
		for _, item := range row.Items {
			add(seenLink, row.Path+linkKeySeparator+item)
		}
	}
	for _, row := range result.aliases {
		add(seenAlias, row.Alias)
	}
	return keys
}

// incompleteWalk reports whether a walk read only part of the volume, so
// the caller skips the prune and keeps the rows. A read error anywhere
// in the walk, at any depth, in a directory, a sidecar, or a file, is one
// signal. A walk that found far fewer items than the catalog holds is the
// other, once the catalog holds more than the ratio floor.
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
//
// A batch that deletes nothing while the query still answers with keys is
// a sweep that cannot end, so it stops with an error rather than spinning
// under the walk lock.
func (c *Catalog) sweep(ctx context.Context, sql string, params []any, del func(ctx context.Context, keys []string) (int, error)) (int, error) {
	removed := 0
	for {
		keys, err := c.queryStrings(ctx, sql, params)
		if err != nil {
			return removed, err
		}
		if len(keys) == 0 {
			return removed, nil
		}
		deleted, err := del(ctx, keys)
		if err != nil {
			return removed, err
		}
		if deleted == 0 {
			return removed, fmt.Errorf("the sweep deleted none of the %d keys it read", len(keys))
		}
		removed += len(keys)
		if len(keys) < pruneBatch {
			return removed, nil
		}
	}
}

// pruneLibrary deletes every catalog row this library holds that the
// current epoch did not mark. It reads the unmarked ids through the query
// API and deletes them by key, the form that needs no delete-time join
// against the local seen table. It returns the count of rows removed.
//
// Every table carries the library, so each sweep scopes itself, and the
// order below is free. It runs the aliases, then the items, then the
// links, then the files.
func pruneLibrary(ctx context.Context, catalog *Catalog, library string, epoch int64) (int, error) {
	removed := 0

	// Every sweep below deletes what this epoch did not mark, so an
	// epoch with no marks at all would delete the whole library. The walk
	// wrote its marks before this prune; an epoch with none is a mark
	// write that did not land, and the rows stand for the next walk.
	marks, err := catalog.countSeen(ctx, epoch)
	if err != nil {
		return removed, err
	}
	if marks == 0 {
		return removed, fmt.Errorf("the walk marked no keys with epoch %d", epoch)
	}

	n, err := catalog.sweep(ctx, itemPruneSQL("aliases", "alias", seenAlias), []any{library, epoch, pruneBatch},
		func(ctx context.Context, keys []string) (int, error) {
			return catalog.DeleteAliases(ctx, library, keys)
		})
	if err != nil {
		return removed, err
	}
	removed += n

	for _, table := range []struct {
		name   string
		delete func(context.Context, string, []string) (int, error)
	}{
		{"movies", catalog.DeleteMovies},
		{"sets", catalog.DeleteSets},
		{"series", catalog.DeleteSeries},
		{"episodes", catalog.DeleteEpisodes},
	} {
		n, err := catalog.sweep(ctx, itemPruneSQL(table.name, "id", seenItem), []any{library, epoch, pruneBatch},
			func(ctx context.Context, keys []string) (int, error) {
				return table.delete(ctx, library, keys)
			})
		if err != nil {
			return removed, err
		}
		removed += n
	}

	n, err = catalog.sweep(ctx, linkPruneSQL(), []any{library, epoch, pruneBatch},
		func(ctx context.Context, keys []string) (int, error) {
			return catalog.DeleteFileItems(ctx, library, fileItemKeys(keys))
		})
	if err != nil {
		return removed, err
	}
	removed += n

	n, err = catalog.sweep(ctx, itemPruneSQL("files", "path", seenFile), []any{library, epoch, pruneBatch},
		func(ctx context.Context, keys []string) (int, error) {
			return catalog.DeleteFiles(ctx, library, keys)
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

// fileItemKeys splits each composite key the link sweep read back into the
// file path and the item id, so the delete names both columns of the row.
func fileItemKeys(keys []string) []fileItemKey {
	links := make([]fileItemKey, len(keys))
	for i, key := range keys {
		path, item, _ := strings.Cut(key, linkKeySeparator)
		links[i] = fileItemKey{Path: path, Item: item}
	}
	return links
}

// linkPruneSQL reads the links this library holds that the current
// epoch did not mark, one bounded batch. A link row carries its own
// library, so the read needs no join to files. It reads the two columns
// joined by the same separator the mark used, so the comparison is one
// string against one string.
func linkPruneSQL() string {
	return `SELECT path || char(31) || item FROM file_items` +
		` WHERE library = ?` +
		` AND '` + seenLink + `' || path || char(31) || item` +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` LIMIT ?`
}

// itemPruneSQL reads the keys of a table this library holds that the
// current epoch did not mark, one bounded batch. table, key, and space are
// constants this package names and never input, so naming them in the SQL
// text carries no injection.
func itemPruneSQL(table, key, space string) string {
	return `SELECT ` + key + ` FROM ` + table +
		` WHERE library = ? AND '` + space + `' || ` + key +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?) LIMIT ?`
}

// pathScopeClause matches the rows of one title folder: the item at the
// folder's own path, and every file and episode under it. It uses a range
// over the path rather than a LIKE, so a folder name that holds a LIKE
// metacharacter still scopes correctly and needs no escape.
func pathScopeClause(column string) string {
	return `(` + column + ` = ? OR (` + column + ` >= ? AND ` + column + ` < ?))`
}

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

	// The alias sweep runs first, and here the order matters: the folder
	// scope of an alias is the folder of the item it names, so the item
	// rows must still stand when this sweep reads them.
	n, err := catalog.sweep(ctx, scopedAliasPruneSQL(), scopedAliasPruneParams(library, folder, epoch),
		func(ctx context.Context, keys []string) (int, error) {
			return catalog.DeleteAliases(ctx, library, keys)
		})
	if err != nil {
		return removed, err
	}
	removed += n

	for _, table := range []struct {
		name   string
		delete func(context.Context, string, []string) (int, error)
	}{
		{"movies", catalog.DeleteMovies},
		{"series", catalog.DeleteSeries},
		{"episodes", catalog.DeleteEpisodes},
	} {
		n, err := catalog.sweep(ctx, scopedItemPruneSQL(table.name, "id", seenItem), scopedItemPruneParams(library, folder, epoch),
			func(ctx context.Context, keys []string) (int, error) {
				return table.delete(ctx, library, keys)
			})
		if err != nil {
			return removed, err
		}
		removed += n
	}

	n, err = catalog.sweep(ctx, scopedLinkPruneSQL(), scopedItemPruneParams(library, folder, epoch),
		func(ctx context.Context, keys []string) (int, error) {
			return catalog.DeleteFileItems(ctx, library, fileItemKeys(keys))
		})
	if err != nil {
		return removed, err
	}
	removed += n

	n, err = catalog.sweep(ctx, scopedItemPruneSQL("files", "path", seenFile), scopedItemPruneParams(library, folder, epoch),
		func(ctx context.Context, keys []string) (int, error) {
			return catalog.DeleteFiles(ctx, library, keys)
		})
	if err != nil {
		return removed, err
	}
	removed += n
	return removed, nil
}

// scopedLinkPruneSQL reads the links under one folder that the current
// epoch did not mark, one bounded batch. A link row carries both the
// library and the file path, so the scope reads the link table alone,
// with the same parameters the scoped item sweeps take.
func scopedLinkPruneSQL() string {
	return `SELECT path || char(31) || item FROM file_items` +
		` WHERE library = ? AND ` + pathScopeClause("path") +
		` AND '` + seenLink + `' || path || char(31) || item` +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` LIMIT ?`
}

// scopedItemPruneSQL reads the keys of one folder's rows in a table that
// the current epoch did not mark, one bounded batch.
func scopedItemPruneSQL(table, key, space string) string {
	return `SELECT ` + key + ` FROM ` + table +
		` WHERE library = ? AND ` + pathScopeClause("path") + ` AND '` + space + `' || ` + key +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?) LIMIT ?`
}

func scopedItemPruneParams(library, folder string, epoch int64) []any {
	params := []any{library}
	params = append(params, pathScopeParams(folder)...)
	return append(params, epoch, pruneBatch)
}

// scopedAliasPruneSQL reads the aliases of one folder's items that the
// current epoch did not mark. The alias row carries the library, so the
// library scopes it directly, and the item tables only narrow it to the
// folder. Each item subquery matches the library as well as the id,
// because an id names one row only inside its own library.
func scopedAliasPruneSQL() string {
	scope := func(table string) string {
		return `SELECT id FROM ` + table + ` WHERE library = ? AND ` + pathScopeClause("path")
	}
	return `SELECT alias FROM aliases` +
		` WHERE library = ?` +
		` AND '` + seenAlias + `' || alias NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` AND item IN (` +
		scope("movies") + ` UNION ` + scope("series") + ` UNION ` + scope("episodes") +
		`) LIMIT ?`
}

func scopedAliasPruneParams(library, folder string, epoch int64) []any {
	params := []any{library, epoch}
	for range 3 {
		params = append(params, library)
		params = append(params, pathScopeParams(folder)...)
	}
	return append(params, pruneBatch)
}
