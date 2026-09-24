package main

// markrows.go holds the marks table: one row per candidate span of one video
// file, keyed by the library, the file's path, and the span's ordinal among
// the file's spans. A file's marks travel with the file, the way its streams
// do. They have no key space of their own in the seen table, and the file
// sweeps remove them.

import (
	"context"
	"strconv"
	"strings"
)

// markKey names one row of the marks table within one library.
type markKey struct {
	Path    string
	Ordinal int
}

// UpsertMarks writes the marks of every video file the walk read, and drops
// the ordinals each file held beyond the count of its fresh set. A file whose
// ledger holds no span keeps no row, so a provider that drops a span drops
// the row with the next write. The drop runs in the same batch, before the
// writes, so a file never keeps a stale span.
func (c *Catalog) UpsertMarks(ctx context.Context, files []fileRow, rows []markRow) (int, error) {
	counts := map[streamFile]int{}
	for _, row := range rows {
		counts[streamFile{library: row.Library, path: row.Path}]++
	}
	statements := []statement{}
	for _, file := range filesOfType(files, fileTypeVideo) {
		statements = append(statements, statement{
			sql:    `DELETE FROM marks WHERE library = ? AND path = ? AND ordinal >= ?`,
			params: []any{file.Library, file.Path, counts[streamFile{library: file.Library, path: file.Path}]},
		})
	}
	for _, row := range rows {
		statements = append(statements, statement{
			sql: `INSERT INTO marks (library, path, ordinal, kind, start_ms, end_ms, source) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, path, ordinal) DO UPDATE SET ` +
				`kind = excluded.kind, start_ms = excluded.start_ms, end_ms = excluded.end_ms, ` +
				`source = excluded.source`,
			params: []any{row.Library, row.Path, row.Ordinal, row.Kind,
				markEnd(row.Start), markEnd(row.End), row.Source},
		})
	}
	return c.apply(ctx, statements)
}

// An open end binds as SQL NULL, because the column holds the absence and
// not a zero.
func markEnd(end *int64) any {
	if end == nil {
		return nil
	}
	return *end
}

// DeleteMarks removes mark rows by their whole primary key.
func (c *Catalog) DeleteMarks(ctx context.Context, library string, keys []markKey) (int, error) {
	statements := make([]statement, len(keys))
	for i, key := range keys {
		statements[i] = statement{
			sql:    `DELETE FROM marks WHERE library = ? AND path = ? AND ordinal = ?`,
			params: []any{library, key.Path, key.Ordinal},
		}
	}
	return c.apply(ctx, statements)
}

// DeleteMarksOfFiles removes every mark of each file the sweep found, so a
// mark never outlives the file it belongs to.
func (c *Catalog) DeleteMarksOfFiles(ctx context.Context, library string, paths []string) (int, error) {
	statements := make([]statement, len(paths))
	for i, path := range paths {
		statements[i] = statement{
			sql:    `DELETE FROM marks WHERE library = ? AND path = ?`,
			params: []any{library, path},
		}
	}
	return c.apply(ctx, statements)
}

// spanKeys splits each composite key the sweep read into the path and the
// ordinal the delete names.
func spanKeys(keys []string) []markKey {
	out := make([]markKey, len(keys))
	for i, key := range keys {
		path, ordinal, _ := strings.Cut(key, linkKeySeparator)
		number, _ := strconv.Atoi(ordinal)
		out[i] = markKey{Path: path, Ordinal: number}
	}
	return out
}

// One bounded batch of one library's mark keys, with the two key columns
// joined the way every sweep of a composite key joins them.
func librarySweepMarkSQL() string {
	return `SELECT path || char(31) || ordinal FROM marks WHERE library = ? LIMIT ?`
}
