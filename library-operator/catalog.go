package main

// catalog.go is the write side of the catalog: a client that posts rows to its
// own sidecar's Corrosion agent over /v1/transactions. Reads come from the
// SQLite file directly and never through this client. Nothing writes the file
// outside the agent, because a write outside it corrupts the CRDT clocks, so
// every write is a batch of statements posted here.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// The transactions endpoint every write posts to.
const transactionsPath = "/v1/transactions"

// The batch ceiling the proof of concept measured: 500 statements per request
// seeded 5000 titles in under half a second.
const maxBatch = 500

// Catalog writes to one Corrosion agent. base is the agent's API address, bound
// to localhost in the pod.
type Catalog struct {
	base string
	http *http.Client
}

// NewCatalog builds a client from the agent's base address and an HTTP client.
// A test hands in an httptest server's base and its client.
func NewCatalog(base string, httpClient *http.Client) *Catalog {
	return &Catalog{base: base, http: httpClient}
}

// statement is one SQL statement and its bound parameters. Every value is a
// parameter and never concatenated into the SQL, so a title with a quote in it
// is data and not syntax.
type statement struct {
	sql    string
	params []any
}

// transactionResponse is the agent's answer: one result per statement, each
// with the rows it changed or the error that stopped it.
type transactionResponse struct {
	Results []transactionResult `json:"results"`
}

type transactionResult struct {
	RowsAffected int    `json:"rows_affected"`
	Error        string `json:"error"`
}

// apply chunks the statements into requests of at most maxBatch and posts each.
// It returns the rows applied so far and stops at the first failure, so a caller
// sees both what landed and what broke.
func (c *Catalog) apply(ctx context.Context, statements []statement) (int, error) {
	applied := 0
	for start := 0; start < len(statements); start += maxBatch {
		end := min(start+maxBatch, len(statements))
		n, err := c.post(ctx, statements[start:end])
		applied += n
		if err != nil {
			return applied, err
		}
	}
	return applied, nil
}

// post sends one batch as a JSON array of [sql, [params...]] entries and reads
// the count applied. A non-2xx status or a per-statement error is a failure the
// caller sees.
func (c *Catalog) post(ctx context.Context, statements []statement) (int, error) {
	body := make([]any, len(statements))
	for i, s := range statements {
		params := s.params
		if params == nil {
			params = []any{}
		}
		body[i] = []any{s.sql, params}
	}
	// The payload is strings, numbers, and slices of them, so it always
	// marshals, and there is no failure here for a caller to answer.
	payload, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+transactionsPath, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer drain(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return 0, fmt.Errorf("catalog transaction: %s: %s", resp.Status, message)
	}

	var result transactionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("catalog transaction: decoding response: %w", err)
	}
	applied := 0
	for _, r := range result.Results {
		if r.Error != "" {
			return applied, fmt.Errorf("catalog transaction: %s", r.Error)
		}
		applied += r.RowsAffected
	}
	return applied, nil
}

// plainItemUpsert is the upsert for an item table that carries only the shared
// header and the slug, which movies and series do. table is a constant this
// package names and never input, so naming it in the SQL text carries no
// injection.
func plainItemUpsert(table string, params []any) statement {
	return statement{
		sql: `INSERT INTO ` + table + ` (id, library, kind, path, title, sort_key, released, added, art, duration, body, slug) ` +
			`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ` +
			`ON CONFLICT(id) DO UPDATE SET ` +
			`library = excluded.library, kind = excluded.kind, path = excluded.path, title = excluded.title, ` +
			`sort_key = excluded.sort_key, released = excluded.released, added = excluded.added, art = excluded.art, ` +
			`duration = excluded.duration, body = excluded.body, slug = excluded.slug`,
		params: params,
	}
}

// itemParams marshals a body and lays the header out in column order.
func itemParams(id, library, kind, path, title, sortKey, released string, added int64, art string, duration int64, body any, slug string) []any {
	payload, _ := json.Marshal(body)
	return []any{id, library, kind, path, title, sortKey, released, added, art, duration, string(payload), slug}
}

// UpsertMovies writes movie rows in place, so a re-walk updates a title rather
// than dropping and recreating it.
func (c *Catalog) UpsertMovies(ctx context.Context, rows []movieRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		params := itemParams(row.Id, row.Library, row.Kind, row.Path, row.Title, row.SortKey, row.Released, row.Added, row.Art, row.Duration, row.Body, row.Slug)
		statements[i] = plainItemUpsert("movies", params)
	}
	return c.apply(ctx, statements)
}

// UpsertSeries writes series rows in place.
func (c *Catalog) UpsertSeries(ctx context.Context, rows []seriesRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		params := itemParams(row.Id, row.Library, row.Kind, row.Path, row.Title, row.SortKey, row.Released, row.Added, row.Art, row.Duration, row.Body, row.Slug)
		statements[i] = plainItemUpsert("series", params)
	}
	return c.apply(ctx, statements)
}

// UpsertEpisodes writes episode rows in place, with the three columns that place
// an episode under its series.
func (c *Catalog) UpsertEpisodes(ctx context.Context, rows []episodeRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		payload, _ := json.Marshal(row.Body)
		statements[i] = statement{
			sql: `INSERT INTO episodes (id, library, kind, path, title, sort_key, released, added, art, duration, body, slug, series, season, episode) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT(id) DO UPDATE SET ` +
				`library = excluded.library, kind = excluded.kind, path = excluded.path, title = excluded.title, ` +
				`sort_key = excluded.sort_key, released = excluded.released, added = excluded.added, art = excluded.art, ` +
				`duration = excluded.duration, body = excluded.body, slug = excluded.slug, ` +
				`series = excluded.series, season = excluded.season, episode = excluded.episode`,
			params: []any{row.Id, row.Library, row.Kind, row.Path, row.Title, row.SortKey, row.Released, row.Added, row.Art, row.Duration, string(payload), row.Slug, row.Series, row.Season, row.Episode},
		}
	}
	return c.apply(ctx, statements)
}

// UpsertFiles writes file rows in place. present is carried as 1 or 0, because
// the column is an integer.
func (c *Catalog) UpsertFiles(ctx context.Context, rows []fileRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		present := 0
		if row.Present {
			present = 1
		}
		statements[i] = statement{
			sql: `INSERT INTO files (path, library, container, video_codec, audio_codec, width, height, size_bytes, duration_ms, trickplay, present, type, role, language, modified) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT(path) DO UPDATE SET ` +
				`library = excluded.library, container = excluded.container, video_codec = excluded.video_codec, ` +
				`audio_codec = excluded.audio_codec, width = excluded.width, height = excluded.height, ` +
				`size_bytes = excluded.size_bytes, duration_ms = excluded.duration_ms, trickplay = excluded.trickplay, present = excluded.present, ` +
				`type = excluded.type, role = excluded.role, language = excluded.language, modified = excluded.modified`,
			params: []any{row.Path, row.Library, row.Container, row.VideoCodec, row.AudioCodec, row.Width, row.Height, row.SizeBytes, row.DurationMs, row.Trickplay, present, row.Type, row.Role, row.Language, row.Modified},
		}
	}
	return c.apply(ctx, statements)
}

// UpsertFileItems writes the many-to-many link, one row per (file, item) pair a
// file carries.
func (c *Catalog) UpsertFileItems(ctx context.Context, rows []fileRow) (int, error) {
	var statements []statement
	for _, row := range rows {
		for _, item := range row.Items {
			statements = append(statements, statement{
				sql:    `INSERT INTO file_items (path, item) VALUES (?, ?) ON CONFLICT(path, item) DO UPDATE SET path = excluded.path`,
				params: []any{row.Path, item},
			})
		}
	}
	return c.apply(ctx, statements)
}

// UpsertAliases writes alias rows in place, so every provider id and the folder
// key resolve to the item.
func (c *Catalog) UpsertAliases(ctx context.Context, rows []aliasRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		statements[i] = statement{
			sql:    `INSERT INTO aliases (alias, item, source) VALUES (?, ?, ?) ON CONFLICT(alias) DO UPDATE SET item = excluded.item, source = excluded.source`,
			params: []any{row.Alias, row.Item, row.Source},
		}
	}
	return c.apply(ctx, statements)
}

// deleteByKey builds one delete per key against a single-column primary key.
// table and column are constants this package names and never input.
func deleteByKey(table, column string, keys []string) []statement {
	statements := make([]statement, len(keys))
	for i, key := range keys {
		statements[i] = statement{
			sql:    `DELETE FROM ` + table + ` WHERE ` + column + ` = ?`,
			params: []any{key},
		}
	}
	return statements
}

// DeleteMovies removes movie rows whose titles left the volume.
func (c *Catalog) DeleteMovies(ctx context.Context, ids []string) (int, error) {
	return c.apply(ctx, deleteByKey("movies", "id", ids))
}

// DeleteSeries removes series rows whose titles left the volume.
func (c *Catalog) DeleteSeries(ctx context.Context, ids []string) (int, error) {
	return c.apply(ctx, deleteByKey("series", "id", ids))
}

// DeleteEpisodes removes episode rows whose files left the volume.
func (c *Catalog) DeleteEpisodes(ctx context.Context, ids []string) (int, error) {
	return c.apply(ctx, deleteByKey("episodes", "id", ids))
}

// DeleteFiles removes file rows whose files left the volume.
func (c *Catalog) DeleteFiles(ctx context.Context, paths []string) (int, error) {
	return c.apply(ctx, deleteByKey("files", "path", paths))
}

// DeleteAliases removes alias rows a re-walk no longer produces.
func (c *Catalog) DeleteAliases(ctx context.Context, aliases []string) (int, error) {
	return c.apply(ctx, deleteByKey("aliases", "alias", aliases))
}

// DeleteFileItemsByPath removes every link row a departed file held, by
// its path. The prune deletes a file by path and knows none of the items
// it linked, so one delete per path clears the whole link.
func (c *Catalog) DeleteFileItemsByPath(ctx context.Context, paths []string) (int, error) {
	return c.apply(ctx, deleteByKey("file_items", "path", paths))
}
