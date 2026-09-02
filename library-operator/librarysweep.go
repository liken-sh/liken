package main

// This file is the sweep the cleanup pod runs: every row one library
// holds, out of all seven replicated tables, through the local agent.
//
// The sweep reads bounded batches of keys and deletes by key, the
// shape pruneLibrary already uses, and not one bare DELETE per
// table. The local harness settled that. One `DELETE FROM movies
// WHERE library = ?` over 47,500 rows took 8.5 seconds, reached one
// peer after 67 seconds, and never reached the other: that peer
// buffered all 47,500 changes as one version with a contiguous seq
// range and no recorded gap, never applied them, stayed divergent
// past ten minutes, and an agent restart did not clear it. The same
// delete in batches of 2,500 reached both peers in about half a
// second. So no transaction here carries more than pruneBatch rows.

import "context"

// librarySweepStep is one table of the sweep: the read that answers
// a batch of the library's keys, and the delete that takes those
// keys out.
type librarySweepStep struct {
	read   string
	delete func(context.Context, []string) (int, error)
}

// SweepLibrary deletes every row one library holds and answers with
// how many rows went. Every key leads with the library, so each read
// and each delete reaches that library's rows and no other's.
//
// The sweep is safe to repeat: a second run reads no keys and
// deletes nothing, which is what lets the cleanup pod re-issue it on
// every tick for as long as the operator keeps the pod up.
func (c *Catalog) SweepLibrary(ctx context.Context, library string) (int, error) {
	removed := 0
	for _, step := range c.librarySweepSteps(library) {
		swept, err := c.sweep(ctx, step.read, []any{library, pruneBatch}, step.delete)
		removed += swept
		if err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// librarySweepSteps is the sweep in the order it runs: the aliases
// and the links that point at an item go before the item rows, and
// the files go last, the same order pruneLibrary deletes in.
func (c *Catalog) librarySweepSteps(library string) []librarySweepStep {
	return []librarySweepStep{
		{librarySweepSQL("aliases", "alias"), func(ctx context.Context, keys []string) (int, error) {
			return c.DeleteAliases(ctx, library, keys)
		}},
		{librarySweepSQL("movies", "id"), func(ctx context.Context, keys []string) (int, error) {
			return c.DeleteMovies(ctx, library, keys)
		}},
		{librarySweepSQL("sets", "id"), func(ctx context.Context, keys []string) (int, error) {
			return c.DeleteSets(ctx, library, keys)
		}},
		{librarySweepSQL("series", "id"), func(ctx context.Context, keys []string) (int, error) {
			return c.DeleteSeries(ctx, library, keys)
		}},
		{librarySweepSQL("episodes", "id"), func(ctx context.Context, keys []string) (int, error) {
			return c.DeleteEpisodes(ctx, library, keys)
		}},
		{librarySweepLinkSQL(), func(ctx context.Context, keys []string) (int, error) {
			return c.DeleteFileItems(ctx, library, fileItemKeys(keys))
		}},
		{librarySweepSQL("files", "path"), func(ctx context.Context, keys []string) (int, error) {
			return c.DeleteFiles(ctx, library, keys)
		}},
	}
}

// librarySweepSQL reads one bounded batch of one library's keys.
// The table and column names are constants this package holds and
// never input, so naming them in the SQL text carries no injection.
func librarySweepSQL(table, column string) string {
	return `SELECT ` + column + ` FROM ` + table + ` WHERE library = ? LIMIT ?`
}

// librarySweepLinkSQL reads the link table's two key columns joined
// by the same separator the prune uses, so one string comes back per
// row and fileItemKeys splits it into the columns the delete names.
func librarySweepLinkSQL() string {
	return `SELECT path || char(31) || item FROM file_items WHERE library = ? LIMIT ?`
}
