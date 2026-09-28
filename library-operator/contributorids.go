package main

// The two tables that let one person keep one entry in .contributors/:
// contributor_ids, which holds every id of every entry, and
// contributor_merges, which holds the entries a merge removed. The walk
// writes both from the store, the prune removes the rows of an entry that
// left it, and the credits and contributor.ids facts read them.

import (
	"context"
	"strings"
)

// One entry that a merge removed: its own path, and the path of the entry
// that stays, both relative to the library root.
type contributorMergeRow struct {
	Library    string
	Path       string
	MergedInto string
}

// The write of one merge record, in place, so a walk that reads it again
// writes the same row.
func (c *Catalog) UpsertContributorMerges(ctx context.Context, rows []contributorMergeRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		statements[i] = statement{
			sql: `INSERT INTO contributor_merges (library, path, merged_into) VALUES (?, ?, ?) ` +
				`ON CONFLICT (library, path) DO UPDATE SET merged_into = excluded.merged_into`,
			params: []any{row.Library, row.Path, row.MergedInto},
		}
	}
	return c.apply(ctx, statements)
}

func (c *Catalog) DeleteContributorMerges(ctx context.Context, library string, paths []string) (int, error) {
	return c.apply(ctx, deleteByKey("contributor_merges", "path", library, paths))
}

// The removes the sweeps make of contributor_ids, each naming every key column.
func (c *Catalog) DeleteContributorIDs(ctx context.Context, library string, keys []contributorIDKey) (int, error) {
	statements := make([]statement, len(keys))
	for i, key := range keys {
		statements[i] = statement{
			sql:    `DELETE FROM contributor_ids WHERE library = ? AND path = ? AND scheme = ?`,
			params: []any{library, key.Path, key.Scheme},
		}
	}
	return c.apply(ctx, statements)
}

// The remove of every id the entries a merge removed hold, from both tables.
// An alias of a removed entry resolves to no one until the entry that stays
// writes its own ids again.
func (c *Catalog) DeletePersonIDs(ctx context.Context, library string, paths []string) (int, error) {
	statements := make([]statement, 0, 2*len(paths))
	for _, path := range paths {
		statements = append(statements,
			statement{sql: `DELETE FROM contributor_aliases WHERE library = ? AND path = ?`, params: []any{library, path}},
			statement{sql: `DELETE FROM contributor_ids WHERE library = ? AND path = ?`, params: []any{library, path}})
	}
	return c.apply(ctx, statements)
}

// The key of one contributor_ids row after the library.
type contributorIDKey struct {
	Path   string
	Scheme string
}

func contributorIDSeenKey(row contributorAliasRow) string {
	return row.Path + linkKeySeparator + row.Scheme
}

func contributorIDKeys(keys []string) []contributorIDKey {
	out := make([]contributorIDKey, len(keys))
	for i, key := range keys {
		path, scheme, _ := strings.Cut(key, linkKeySeparator)
		out[i] = contributorIDKey{Path: path, Scheme: scheme}
	}
	return out
}

// The rows of contributor_ids this library holds that the current epoch did
// not mark, one bounded batch, with the two key columns joined the way the
// mark joined them.
func contributorIDPruneSQL() string {
	return `SELECT path || char(31) || scheme FROM contributor_ids` +
		` WHERE library = ?` +
		` AND '` + seenContributorID + `' || path || char(31) || scheme` +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` LIMIT ?`
}

func librarySweepContributorIDSQL() string {
	return `SELECT path || char(31) || scheme FROM contributor_ids WHERE library = ? LIMIT ?`
}

// Every entry that holds one id, the shortest path first. The shortest path is
// the entry at the plain slug where the store holds two entries of one person
// before they merge, because the other one carries an id suffix.
func (c *Catalog) contributorsWithID(ctx context.Context, library, scheme, id string) ([]string, error) {
	return c.queryStrings(ctx, `SELECT path FROM contributor_ids `+
		`WHERE library = ? AND scheme = ? AND id = ? ORDER BY length(path), path`,
		[]any{library, scheme, id})
}

// How many credits of one library name one entry. The credits index on
// (library, contributor) answers it.
func (c *Catalog) creditsNaming(ctx context.Context, library, path string) (int, error) {
	return c.queryInt(ctx, `SELECT count(*) FROM credits WHERE library = ? AND contributor = ?`,
		[]any{library, path})
}
