package main

// The people the catalog holds, derived from the volume alone: the walk of
// .contributors/, the three tables its files become, and the writes and
// deletes that keep them. A lost catalog gets every one of these rows back
// from the volume on the next walk.

import (
	"context"
	"errors"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// One person as the contributors table holds them: the directory that names
// them, relative to the library root, the name a person reads, the two dates,
// and whether the two files are beside the entry. The gap query of each
// contributor fact reads those two marks.
type contributorRow struct {
	Library   string
	Path      string
	Name      string
	Born      string
	Died      string
	Biography bool
	Headshot  bool
}

// One id of one person, in the shape the item aliases take: the scheme and the
// id name the person, and the path resolves to the row. One person joins
// across libraries by any id two of them share.
type contributorAliasRow struct {
	Library string
	Scheme  string
	ID      string
	Path    string
}

// One credited person on one title, as credits.yaml states them. The billing
// order is the key beside the item, because it is the one thing a title gives
// each of its people exactly once, and a person with no entry in
// .contributors/ still holds a row with a name and a part.
type creditRow struct {
	Library     string
	Item        string
	Contributor string
	Name        string
	Part        string
	Role        string
	Billing     int
}

// The ledger files a person's own directory holds. They are not in likenFacts,
// because a title folder holds none of them, and a walk that read three files
// per title that are never there would cost a round trip each on a network
// volume.
var contributorLedgerFacts = []string{
	factContributorIDs, factContributorBiography, factContributorHeadshot,
}

// The walk of one library's .contributors/ store, one person per result. The
// walk of the titles skips every dot directory, and this one is the exception,
// read after the titles and read only. The full walk feeds each person through
// the same buffer as the titles, because a store holds tens of thousands of
// people and their rows do not fit the scanner's memory at once. A store that
// is not there is no error, because a library whose credits fact has not run
// yet holds none.
func walkContributors(root, library string) iter.Seq[*walkResult] {
	return func(yield func(*walkResult) bool) {
		store := filepath.Join(root, contributorsDirectory)
		letters, err := os.ReadDir(store)
		if errors.Is(err, fs.ErrNotExist) {
			return
		}
		if err != nil {
			yield(readFailed(err))
			return
		}
		for _, letter := range letters {
			if !letter.IsDir() {
				continue
			}
			people, err := os.ReadDir(filepath.Join(store, letter.Name()))
			if err != nil && !yield(readFailed(err)) {
				return
			}
			for _, person := range people {
				if !person.IsDir() {
					continue
				}
				result := &walkResult{}
				readContributorFolder(root, library, filepath.Join(store, letter.Name(), person.Name()), result)
				if !yield(result) {
					return
				}
			}
		}
	}
}

// readFailed is the result of a directory the walk could not read: no rows,
// and the incomplete mark.
func readFailed(err error) *walkResult {
	result := &walkResult{}
	result.noteReadError(err)
	return result
}

// One person's directory into its rows. A directory with no contributor.yaml
// is not a person and writes no row, so a stray directory under the store is
// left out of the catalog.
func readContributorFolder(root, library, dir string, result *walkResult) {
	held, data, err := readContributorFile(filepath.Join(dir, contributorFileName))
	result.noteReadError(err)
	if err != nil || data == nil {
		return
	}
	path := relativePath(root, dir)
	biography, err := fileExists(filepath.Join(dir, contributorBiographyName))
	result.noteReadError(err)
	headshot, err := fileExists(filepath.Join(dir, contributorHeadshotName))
	result.noteReadError(err)

	result.contributors = append(result.contributors, contributorRow{
		Library: library, Path: path, Name: held.Name,
		Born: held.Born, Died: held.Died,
		Biography: biography, Headshot: headshot,
	})
	for _, scheme := range sortedKeys(held.IDs) {
		if id := held.IDs[scheme]; id != "" {
			result.contributorAliases = append(result.contributorAliases, contributorAliasRow{
				Library: library, Scheme: scheme, ID: id, Path: path,
			})
		}
	}
	readLikenSidecar(likenSidecar{
		root: root, dir: dir, library: library, item: path, facts: contributorLedgerFacts,
	}, result)
}

// The credits of one title, lifted out of the ledger the credits fact wrote.
// The row carries the person's directory as the fact recorded it, so the join
// to the contributors table is one column against one column.
func creditRows(library, item string, credits []creditEntry) []creditRow {
	rows := make([]creditRow, 0, len(credits))
	for _, credit := range credits {
		if strings.TrimSpace(credit.Name) == "" {
			continue
		}
		rows = append(rows, creditRow{
			Library: library, Item: item, Contributor: credit.Contributor,
			Name: credit.Name, Part: credit.Part, Role: credit.Role, Billing: credit.Order,
		})
	}
	return rows
}

// The write of one person's row, in place, so a re-walk updates the person
// rather than dropping and creating them.
func (c *Catalog) UpsertContributors(ctx context.Context, rows []contributorRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		statements[i] = statement{
			sql: `INSERT INTO contributors (library, path, name, born, died, biography, headshot) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, path) DO UPDATE SET ` +
				`name = excluded.name, born = excluded.born, died = excluded.died, ` +
				`biography = excluded.biography, headshot = excluded.headshot`,
			params: []any{row.Library, row.Path, row.Name, row.Born, row.Died,
				presentValue(row.Biography), presentValue(row.Headshot)},
		}
	}
	return c.apply(ctx, statements)
}

// A mark the catalog holds as an integer, because the column is one.
func presentValue(held bool) int {
	if held {
		return 1
	}
	return 0
}

// The write of one id, in place. The scheme and the id are the key beside the
// library, so an id that moves to another person resolves to the person who
// holds it now.
func (c *Catalog) UpsertContributorAliases(ctx context.Context, rows []contributorAliasRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		statements[i] = statement{
			sql: `INSERT INTO contributor_aliases (library, scheme, id, path) VALUES (?, ?, ?, ?) ` +
				`ON CONFLICT (library, scheme, id) DO UPDATE SET path = excluded.path`,
			params: []any{row.Library, row.Scheme, row.ID, row.Path},
		}
	}
	return c.apply(ctx, statements)
}

// The write of one credit, in place, keyed by the title and the billing order,
// so a re-cast of one slot updates the row.
func (c *Catalog) UpsertCredits(ctx context.Context, rows []creditRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		statements[i] = statement{
			sql: `INSERT INTO credits (library, item, billing, contributor, name, part, role) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, item, billing) DO UPDATE SET ` +
				`contributor = excluded.contributor, name = excluded.name, ` +
				`part = excluded.part, role = excluded.role`,
			params: []any{row.Library, row.Item, row.Billing, row.Contributor, row.Name,
				row.Part, row.Role},
		}
	}
	return c.apply(ctx, statements)
}

// The removes the sweep makes, each naming every key column of its own table,
// so a delete reaches one row and never another library's.
func (c *Catalog) DeleteContributors(ctx context.Context, library string, paths []string) (int, error) {
	return c.apply(ctx, deleteByKey("contributors", "path", library, paths))
}

func (c *Catalog) DeleteContributorAliases(ctx context.Context, library string, keys []contributorAliasKey) (int, error) {
	statements := make([]statement, len(keys))
	for i, key := range keys {
		statements[i] = statement{
			sql:    `DELETE FROM contributor_aliases WHERE library = ? AND scheme = ? AND id = ?`,
			params: []any{library, key.Scheme, key.ID},
		}
	}
	return c.apply(ctx, statements)
}

func (c *Catalog) DeleteCredits(ctx context.Context, library string, keys []creditKey) (int, error) {
	statements := make([]statement, len(keys))
	for i, key := range keys {
		statements[i] = statement{
			sql:    `DELETE FROM credits WHERE library = ? AND item = ? AND billing = ?`,
			params: []any{library, key.Item, key.Billing},
		}
	}
	return c.apply(ctx, statements)
}

// The two composite keys the sweeps read back, each of them the row's own key
// columns after the library.
type contributorAliasKey struct {
	Scheme string
	ID     string
}

type creditKey struct {
	Item    string
	Billing int
}

// The keys travel through the sweep as one string, joined by the separator no
// path, scheme, or id holds, the way a link key does.
func contributorAliasSeenKey(row contributorAliasRow) string {
	return row.Scheme + linkKeySeparator + row.ID
}

func creditSeenKey(row creditRow) string {
	return row.Item + linkKeySeparator + strconv.Itoa(row.Billing)
}

func contributorAliasKeys(keys []string) []contributorAliasKey {
	out := make([]contributorAliasKey, len(keys))
	for i, key := range keys {
		scheme, id, _ := strings.Cut(key, linkKeySeparator)
		out[i] = contributorAliasKey{Scheme: scheme, ID: id}
	}
	return out
}

func creditKeys(keys []string) []creditKey {
	out := make([]creditKey, len(keys))
	for i, key := range keys {
		item, billing, _ := strings.Cut(key, linkKeySeparator)
		number, _ := strconv.Atoi(billing)
		out[i] = creditKey{Item: item, Billing: number}
	}
	return out
}

// The reads of the rows this library holds that the current epoch did not
// mark, one bounded batch, with the two key columns joined the way the mark
// joined them. SQL rebuilds the identical string with char(31).
func contributorAliasPruneSQL() string {
	return `SELECT scheme || char(31) || id FROM contributor_aliases` +
		` WHERE library = ?` +
		` AND '` + seenContributorAlias + `' || scheme || char(31) || id` +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` LIMIT ?`
}

func creditPruneSQL() string {
	return `SELECT item || char(31) || billing FROM credits` +
		` WHERE library = ?` +
		` AND '` + seenCredit + `' || item || char(31) || billing` +
		` NOT IN (SELECT id FROM seen WHERE epoch = ?)` +
		` LIMIT ?`
}
