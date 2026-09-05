package main

// franchiserows.go writes the three franchise tables and sweeps them, the
// way genres.go writes and sweeps the genres table. The franchises row is
// an item row, so it marks and sweeps under the item key space beside the
// movies and the series. The members and the runs key on the franchise and
// the position, so each carries a key space of its own.

import (
	"context"
	"strconv"
	"strings"
)

// The two key spaces the members and the runs mark under. A member key is
// the franchise and the position joined, and a run key adds the season and
// the episode.
const (
	seenFranchiseMember = "franchise-member:"
	seenFranchiseRun    = "franchise-run:"
)

// UpsertFranchises writes the franchises rows. A repeat write updates the
// row in place, so a re-walk of an unchanged commit changes no row and
// broadcasts nothing. The conflict target is the whole primary key, and the
// update names no key column, because cr-sqlite reads a change to a key
// column as a delete and a create.
func (c *Catalog) UpsertFranchises(ctx context.Context, rows []franchiseRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		params := itemParams(row.Library, row.Id, row.Kind, row.Path, row.Title, row.SortKey,
			row.Released, row.Added, row.Art, row.Duration, row.Body, row.Slug)
		statements[i] = statement{
			sql: `INSERT INTO franchises (library, id, kind, path, title, sort_key, released, added, art, duration, body, slug, arts) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, id) DO UPDATE SET ` +
				`kind = excluded.kind, path = excluded.path, title = excluded.title, ` +
				`sort_key = excluded.sort_key, released = excluded.released, added = excluded.added, ` +
				`art = excluded.art, duration = excluded.duration, body = excluded.body, ` +
				`slug = excluded.slug, arts = excluded.arts`,
			params: append(params, artsParam(row.Arts)),
		}
	}
	return c.apply(ctx, statements)
}

// UpsertFranchiseMembers writes one member row per entry, keyed by the
// franchise and the position.
func (c *Catalog) UpsertFranchiseMembers(ctx context.Context, rows []franchiseMemberRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		statements[i] = statement{
			sql: `INSERT INTO franchise_members (library, franchise, position, kind, alias, title, released, release_year, timed, time_from, time_to, universes) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, franchise, position) DO UPDATE SET ` +
				`kind = excluded.kind, alias = excluded.alias, title = excluded.title, ` +
				`released = excluded.released, release_year = excluded.release_year, ` +
				`timed = excluded.timed, time_from = excluded.time_from, time_to = excluded.time_to, ` +
				`universes = excluded.universes`,
			params: []any{row.Library, row.Franchise, row.Position, row.Kind, row.Alias, row.Title,
				row.Released, row.ReleaseYear, row.Timed, row.TimeFrom, row.TimeTo, row.Universes},
		}
	}
	return c.apply(ctx, statements)
}

// UpsertFranchiseRuns writes the run rows. Every column of a run row is a
// key column, so the row carries nothing to update and a repeat write
// broadcasts nothing.
func (c *Catalog) UpsertFranchiseRuns(ctx context.Context, rows []franchiseRunRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		statements[i] = statement{
			sql: `INSERT INTO franchise_runs (library, franchise, position, season, episode) ` +
				`VALUES (?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, franchise, position, season, episode) DO NOTHING`,
			params: []any{row.Library, row.Franchise, row.Position, row.Season, row.Episode},
		}
	}
	return c.apply(ctx, statements)
}

// DeleteFranchises removes the franchise rows whose directory left the
// repository.
func (c *Catalog) DeleteFranchises(ctx context.Context, library string, ids []string) (int, error) {
	return c.apply(ctx, deleteByKey("franchises", "id", library, ids))
}

// franchiseMemberKey is the two key columns after the library, as the
// sweep reads them back.
type franchiseMemberKey struct {
	Franchise string
	Position  int
}

// franchiseRunKey is the four key columns after the library, as the sweep
// reads them back.
type franchiseRunKey struct {
	Franchise string
	Position  int
	Season    int
	Episode   int
}

// DeleteFranchiseMembers names every key column, so a sweep takes exactly
// the rows it read. DeleteFranchiseRuns does the same for the runs.
func (c *Catalog) DeleteFranchiseMembers(ctx context.Context, library string, keys []franchiseMemberKey) (int, error) {
	statements := make([]statement, len(keys))
	for i, key := range keys {
		statements[i] = statement{
			sql:    `DELETE FROM franchise_members WHERE library = ? AND franchise = ? AND position = ?`,
			params: []any{library, key.Franchise, key.Position},
		}
	}
	return c.apply(ctx, statements)
}

func (c *Catalog) DeleteFranchiseRuns(ctx context.Context, library string, keys []franchiseRunKey) (int, error) {
	statements := make([]statement, len(keys))
	for i, key := range keys {
		statements[i] = statement{
			sql: `DELETE FROM franchise_runs WHERE library = ? AND franchise = ? AND position = ?` +
				` AND season = ? AND episode = ?`,
			params: []any{library, key.Franchise, key.Position, key.Season, key.Episode},
		}
	}
	return c.apply(ctx, statements)
}

// The key travels through the sweep as one string, joined by the separator
// no id holds, the way a genre key does. The two functions after these
// read the strings back.
func franchiseMemberSeenKey(row franchiseMemberRow) string {
	return row.Franchise + linkKeySeparator + strconv.Itoa(row.Position)
}

func franchiseRunSeenKey(row franchiseRunRow) string {
	return row.Franchise + linkKeySeparator + strconv.Itoa(row.Position) +
		linkKeySeparator + strconv.Itoa(row.Season) + linkKeySeparator + strconv.Itoa(row.Episode)
}

func franchiseMemberKeys(keys []string) []franchiseMemberKey {
	out := make([]franchiseMemberKey, len(keys))
	for i, key := range keys {
		parts := strings.Split(key, linkKeySeparator)
		out[i] = franchiseMemberKey{Franchise: parts[0], Position: keyNumber(parts, 1)}
	}
	return out
}

func franchiseRunKeys(keys []string) []franchiseRunKey {
	out := make([]franchiseRunKey, len(keys))
	for i, key := range keys {
		parts := strings.Split(key, linkKeySeparator)
		out[i] = franchiseRunKey{
			Franchise: parts[0],
			Position:  keyNumber(parts, 1),
			Season:    keyNumber(parts, 2),
			Episode:   keyNumber(parts, 3),
		}
	}
	return out
}

// keyNumber reads one number out of a joined key, and 0 where the key is
// short. The sweep reads back the keys the mark wrote, so a short key
// cannot happen, and the delete refuses to guess at one.
func keyNumber(parts []string, index int) int {
	if index >= len(parts) {
		return 0
	}
	number, _ := strconv.Atoi(parts[index])
	return number
}

// franchiseMemberPruneSQL selects the members this library holds that the
// current epoch did not mark, one bounded batch, joined the way the mark
// joined them. franchiseRunPruneSQL does the same for the runs.
func franchiseMemberPruneSQL() string {
	return `SELECT franchise || char(31) || position FROM franchise_members` +
		` WHERE library = ?` +
		` AND '` + seenFranchiseMember + `' || franchise || char(31) || position` +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` LIMIT ?`
}

func franchiseRunPruneSQL() string {
	return `SELECT franchise || char(31) || position || char(31) || season || char(31) || episode` +
		` FROM franchise_runs WHERE library = ?` +
		` AND '` + seenFranchiseRun + `' || franchise || char(31) || position` +
		` || char(31) || season || char(31) || episode` +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` LIMIT ?`
}

// librarySweepFranchiseMemberSQL selects one bounded batch of one
// library's members for the whole-library sweep, and
// librarySweepFranchiseRunSQL the runs.
func librarySweepFranchiseMemberSQL() string {
	return `SELECT franchise || char(31) || position FROM franchise_members WHERE library = ? LIMIT ?`
}

func librarySweepFranchiseRunSQL() string {
	return `SELECT franchise || char(31) || position || char(31) || season || char(31) || episode` +
		` FROM franchise_runs WHERE library = ? LIMIT ?`
}
