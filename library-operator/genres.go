package main

// genres.go is the genres table: one row per title and genre, in the order
// the sidecar lists them. The body column holds the same genres as JSON, which
// no index reaches, so a read over one genre needs this table. The order is
// kept because the sidecar's first genre is the title's main genre. The rows
// are derived from the sidecar, so the walk writes them and the mark-and-sweep
// prune takes them with the title, the way it takes credits. This file holds
// the row, the writes and deletes, and the sweep reads.

import (
	"context"
	"strconv"
	"strings"
)

// One genre of one title. The rank is its position in the sidecar's list,
// from zero, and it is the key beside the item, the way a credit keys on its
// billing.
type genreRow struct {
	Library string
	Item    string
	Rank    int
	Genre   string
}

// The rows of one title, in the sidecar's order.
func genreRows(library, item string, genres []string) []genreRow {
	rows := make([]genreRow, 0, len(genres))
	for rank, genre := range genres {
		rows = append(rows, genreRow{Library: library, Item: item, Rank: rank, Genre: genre})
	}
	return rows
}

// A repeat write updates the genre in place, keyed by the title and the rank,
// so a sidecar that reorders its genres updates the rows.
func (c *Catalog) UpsertGenres(ctx context.Context, rows []genreRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		statements[i] = statement{
			sql: `INSERT INTO genres (library, item, rank, genre) VALUES (?, ?, ?, ?) ` +
				`ON CONFLICT (library, item, rank) DO UPDATE SET genre = excluded.genre`,
			params: []any{row.Library, row.Item, row.Rank, row.Genre},
		}
	}
	return c.apply(ctx, statements)
}

// The delete names every key column, so a sweep takes the rows it read and
// no other library's.
func (c *Catalog) DeleteGenres(ctx context.Context, library string, keys []genreKey) (int, error) {
	statements := make([]statement, len(keys))
	for i, key := range keys {
		statements[i] = statement{
			sql:    `DELETE FROM genres WHERE library = ? AND item = ? AND rank = ?`,
			params: []any{library, key.Item, key.Rank},
		}
	}
	return c.apply(ctx, statements)
}

// The two key columns after the library, as the sweep reads them back.
type genreKey struct {
	Item string
	Rank int
}

// The key travels through the sweep as one string, joined by the separator
// no id holds, the way a credit key does.
func genreSeenKey(row genreRow) string {
	return row.Item + linkKeySeparator + strconv.Itoa(row.Rank)
}

func genreKeys(keys []string) []genreKey {
	out := make([]genreKey, len(keys))
	for i, key := range keys {
		item, rank, _ := strings.Cut(key, linkKeySeparator)
		number, _ := strconv.Atoi(rank)
		out[i] = genreKey{Item: item, Rank: number}
	}
	return out
}

// The genres this library holds that the current epoch did not mark, one
// bounded batch, joined the way the mark joined them.
func genrePruneSQL() string {
	return `SELECT item || char(31) || rank FROM genres` +
		` WHERE library = ?` +
		` AND '` + seenGenre + `' || item || char(31) || rank` +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` LIMIT ?`
}

// A rescan reaches one folder's genres through the movie or series row the
// folder holds, so this sweep runs before the item sweeps take that row.
func scopedGenrePruneSQL() string {
	scope := func(table string) string {
		return `SELECT id FROM ` + table + ` WHERE library = ? AND ` + pathScopeClause("path")
	}
	return `SELECT item || char(31) || rank FROM genres` +
		` WHERE library = ?` +
		` AND '` + seenGenre + `' || item || char(31) || rank` +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` AND item IN (` + scope("movies") + ` UNION ` + scope("series") + `)` +
		` LIMIT ?`
}

func scopedGenrePruneParams(library, folder string, epoch int64) []any {
	params := []any{library, epoch}
	for range 2 {
		params = append(params, library)
		params = append(params, pathScopeParams(folder)...)
	}
	return append(params, pruneBatch)
}

// One bounded batch of one library's genres, for the whole-library sweep.
func librarySweepGenreSQL() string {
	return `SELECT item || char(31) || rank FROM genres WHERE library = ? LIMIT ?`
}
