package main

// The statements a fact writes its own columns with. Every column has one
// owner, plan 34's table, and each statement here names that owner's columns
// and no other, so two phases that write one row at the same time never race
// on a column. Corrosion merges a row per column, last writer wins, which is
// what makes that safe. The scan alone writes whole rows.

import (
	"context"
	"encoding/json"
)

// The item table a kind's titles are in.
func itemTable(kind string) string {
	if kind == libraryKindSeries {
		return "series"
	}
	return "movies"
}

// The probe's columns of a file: the streams ffprobe read.
func (c *Catalog) UpdateFileStreams(ctx context.Context, rows []fileRow) (int, error) {
	statements := make([]statement, 0, len(rows))
	for _, row := range rows {
		statements = append(statements, statement{
			sql: `UPDATE files SET video_codec = ?, audio_codec = ?, width = ?, height = ?, duration_ms = ? ` +
				`WHERE library = ? AND path = ?`,
			params: []any{row.VideoCodec, row.AudioCodec, row.Width, row.Height, row.DurationMs, row.Library, row.Path},
		})
	}
	return c.apply(ctx, statements)
}

// The trickplay column of a file, the one column the trickplay fact owns.
func (c *Catalog) UpdateFileTrickplay(ctx context.Context, rows []fileRow) (int, error) {
	statements := make([]statement, 0, len(rows))
	for _, row := range rows {
		statements = append(statements, statement{
			sql:    `UPDATE files SET trickplay = ? WHERE library = ? AND path = ?`,
			params: []any{row.Trickplay, row.Library, row.Path},
		})
	}
	return c.apply(ctx, statements)
}

// The arrived column, the arrival fact's own, from the ledger the fact
// wrote.
func (c *Catalog) UpdateFileArrived(ctx context.Context, rows []fileRow) (int, error) {
	statements := make([]statement, 0, len(rows))
	for _, row := range rows {
		statements = append(statements, statement{
			sql:    `UPDATE files SET arrived = ? WHERE library = ? AND path = ?`,
			params: []any{row.Arrived, row.Library, row.Path},
		})
	}
	return c.apply(ctx, statements)
}

// One item's update: the key, and one value per column the caller names.
type itemUpdate struct {
	Library string
	Id      string
	Values  []any
}

// One UPDATE per item on the named columns of one item table. The table and
// the columns are constants this package names and never input, so naming
// them in the SQL text carries no injection.
func (c *Catalog) updateItems(ctx context.Context, table string, columns []string, rows []itemUpdate) (int, error) {
	set := ""
	for i, column := range columns {
		if i > 0 {
			set += ", "
		}
		set += column + " = ?"
	}
	statements := make([]statement, 0, len(rows))
	for _, row := range rows {
		params := append(append([]any{}, row.Values...), row.Library, row.Id)
		statements = append(statements, statement{
			sql:    `UPDATE ` + table + ` SET ` + set + ` WHERE library = ? AND id = ?`,
			params: params,
		})
	}
	return c.apply(ctx, statements)
}

// The duration of an item, which the probe owns where the sidecar states no
// runtime of its own.
func (c *Catalog) UpdateItemDurations(ctx context.Context, table string, rows []itemUpdate) (int, error) {
	return c.updateItems(ctx, table, []string{"duration"}, rows)
}

// The added column of the items a folder holds, the arrival fact's column,
// from a re-read of the ledger it wrote.
func (c *Catalog) UpdateItemAdded(ctx context.Context, table string, rows []itemUpdate) (int, error) {
	return c.updateItems(ctx, table, []string{"added"}, rows)
}

// The body and the nfo_facts of a title, the nfo phase's columns, from a
// re-parse of the sidecar it wrote.
func (c *Catalog) UpdateItemBodies(ctx context.Context, table string, rows []itemUpdate) (int, error) {
	return c.updateItems(ctx, table, []string{"body", "nfo_facts"}, rows)
}

// The primary art and the art list of an item, the art phase's columns.
func (c *Catalog) UpdateItemArt(ctx context.Context, table string, rows []itemUpdate) (int, error) {
	return c.updateItems(ctx, table, []string{"art", "arts"}, rows)
}

func bodyUpdate(library, id string, body any, facts string) itemUpdate {
	payload, _ := json.Marshal(body)
	return itemUpdate{Library: library, Id: id, Values: []any{string(payload), facts}}
}

func artUpdate(library, id, art string, arts []string) itemUpdate {
	return itemUpdate{Library: library, Id: id, Values: []any{art, artsParam(arts)}}
}

// The credits of one item as a set: the old rows leave and the new ones land
// in one apply. The billing order is the key, and a person who moved in the
// order would otherwise hold two rows.
func (c *Catalog) ReplaceCredits(ctx context.Context, library, item string, rows []creditRow) (int, error) {
	statements := []statement{{
		sql:    `DELETE FROM credits WHERE library = ? AND item = ?`,
		params: []any{library, item},
	}}
	for _, row := range rows {
		statements = append(statements, statement{
			sql: `INSERT INTO credits (library, item, billing, contributor, name, part, role) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?)`,
			params: []any{row.Library, row.Item, row.Billing, row.Contributor, row.Name, row.Part, row.Role},
		})
	}
	return c.apply(ctx, statements)
}

// A person's row as the credits fact creates it, and no write where the row
// exists, because the contributors phase owns the columns from then on.
func (c *Catalog) InsertContributors(ctx context.Context, rows []contributorRow) (int, error) {
	statements := make([]statement, 0, len(rows))
	for _, row := range rows {
		statements = append(statements, statement{
			sql: `INSERT INTO contributors (library, path, name, born, died, biography, headshot) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (library, path) DO NOTHING`,
			params: []any{row.Library, row.Path, row.Name, row.Born, row.Died, flag(row.Biography), flag(row.Headshot)},
		})
	}
	return c.apply(ctx, statements)
}

// The contributors phase's columns of a person: what the ids fact and the two
// file facts fill.
func (c *Catalog) UpdateContributorFacts(ctx context.Context, rows []contributorRow) (int, error) {
	statements := make([]statement, 0, len(rows))
	for _, row := range rows {
		statements = append(statements, statement{
			sql: `UPDATE contributors SET born = ?, died = ?, biography = ?, headshot = ? ` +
				`WHERE library = ? AND path = ?`,
			params: []any{row.Born, row.Died, flag(row.Biography), flag(row.Headshot), row.Library, row.Path},
		})
	}
	return c.apply(ctx, statements)
}

func flag(held bool) int {
	if held {
		return 1
	}
	return 0
}
