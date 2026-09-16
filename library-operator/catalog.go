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
//
// Version is the db version this write left on the writing agent,
// and null where the write changed nothing. ActorID is that agent's own
// id. Together they name the write a standing copy has to hold, which is
// how a Job proves its rows landed.
type transactionResponse struct {
	Results []transactionResult `json:"results"`
	Version *int64              `json:"version"`
	ActorID string              `json:"actor_id"`
}

type transactionResult struct {
	RowsAffected int    `json:"rows_affected"`
	Error        string `json:"error"`
}

// What one write left behind: the rows it applied, the agent that
// applied them, and the db version that agent gave the last request of the
// batch. A caller that only counts rows reads the count and drops the rest.
type writeMade struct {
	applied int
	actor   string
	version int64
}

// apply chunks the statements into requests of at most maxBatch and posts each.
// It returns the rows applied so far and stops at the first failure, so a caller
// sees both what landed and what broke.
func (c *Catalog) apply(ctx context.Context, statements []statement) (int, error) {
	made, err := c.applyMade(ctx, statements)
	return made.applied, err
}

// ApplyMade is apply with the write's own identity kept: the actor
// and the newest version the batch made. A Job writes its run through this
// one, because the two values are what a confirmer proves it holds.
func (c *Catalog) applyMade(ctx context.Context, statements []statement) (writeMade, error) {
	made := writeMade{}
	for start := 0; start < len(statements); start += maxBatch {
		end := min(start+maxBatch, len(statements))
		chunk, err := c.post(ctx, statements[start:end])
		made.applied += chunk.applied
		if chunk.actor != "" {
			made.actor = chunk.actor
		}
		made.version = max(made.version, chunk.version)
		if err != nil {
			return made, err
		}
	}
	return made, nil
}

// post sends one batch as a JSON array of [sql, [params...]] entries and reads
// the count applied. A non-2xx status or a per-statement error is a failure the
// caller sees.
func (c *Catalog) post(ctx context.Context, statements []statement) (writeMade, error) {
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
		return writeMade{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return writeMade{}, err
	}
	defer drain(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return writeMade{}, fmt.Errorf("catalog transaction: %s: %s", resp.Status, message)
	}

	var result transactionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return writeMade{}, fmt.Errorf("catalog transaction: decoding response: %w", err)
	}
	// One result per statement is the contract, so a short answer is a
	// failure and never a batch that applied.
	if len(result.Results) != len(statements) {
		return writeMade{}, fmt.Errorf("catalog transaction: %d results for %d statements",
			len(result.Results), len(statements))
	}
	made := writeMade{actor: result.ActorID}
	if result.Version != nil {
		made.version = *result.Version
	}
	for _, r := range result.Results {
		if r.Error != "" {
			return made, fmt.Errorf("catalog transaction: %s", r.Error)
		}
		made.applied += r.RowsAffected
	}
	return made, nil
}

// plainItemUpsert is the upsert for an item table that carries only the shared
// header and the slug, which series and sets do. Movies and episodes each
// carry a column beyond the header and have an upsert of their own. table is
// a constant this package names and never input, so naming it in the SQL text
// carries no injection.
//
// The conflict target is the whole primary key, (library, id), and the
// update names no primary-key column, because cr-sqlite reads a change
// to a key column as a delete and a create.
func plainItemUpsert(table string, params []any) statement {
	return statement{
		sql: `INSERT INTO ` + table + ` (library, id, kind, path, title, sort_key, released, added, art, duration, body, slug) ` +
			`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ` +
			`ON CONFLICT (library, id) DO UPDATE SET ` +
			`kind = excluded.kind, path = excluded.path, title = excluded.title, ` +
			`sort_key = excluded.sort_key, released = excluded.released, added = excluded.added, art = excluded.art, ` +
			`duration = excluded.duration, body = excluded.body, slug = excluded.slug`,
		params: params,
	}
}

// itemParams marshals a body and lays the header out in column order.
//
// The library is the first parameter of every statement this file
// writes, because it leads every key.
func itemParams(library, id, kind, path, title, sortKey, released string, added int64, art string, duration int64, body any, slug string) []any {
	payload, _ := json.Marshal(body)
	return []any{library, id, kind, path, title, sortKey, released, added, art, duration, string(payload), slug}
}

// The arts column: the list as JSON, and an empty list for an item with none,
// so a reader always parses a list.
func artsParam(arts []string) string {
	if len(arts) == 0 {
		return "[]"
	}
	payload, _ := json.Marshal(arts)
	return string(payload)
}

// UpsertMovies writes movie rows in place, so a re-walk updates a title rather
// than dropping and recreating it. The movies table adds set_id after the
// header, so this upsert names its own columns.
func (c *Catalog) UpsertMovies(ctx context.Context, rows []movieRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		params := itemParams(row.Library, row.Id, row.Kind, row.Path, row.Title, row.SortKey, row.Released, row.Added, row.Art, row.Duration, row.Body, row.Slug)
		statements[i] = statement{
			sql: `INSERT INTO movies (library, id, kind, path, title, sort_key, released, added, art, duration, body, slug, set_id, nfo_facts, arts) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, id) DO UPDATE SET ` +
				`kind = excluded.kind, path = excluded.path, title = excluded.title, ` +
				`sort_key = excluded.sort_key, released = excluded.released, added = excluded.added, art = excluded.art, ` +
				`duration = excluded.duration, body = excluded.body, slug = excluded.slug, ` +
				`set_id = excluded.set_id, nfo_facts = excluded.nfo_facts, arts = excluded.arts`,
			params: append(params, row.SetID, row.NFOFacts, artsParam(row.Arts)),
		}
	}
	return c.apply(ctx, statements)
}

// UpsertSets writes the derived set rows in place, so a walk that reads a new
// member updates the set rather than dropping and recreating it.
func (c *Catalog) UpsertSets(ctx context.Context, rows []setRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		params := itemParams(row.Library, row.Id, row.Kind, row.Path, row.Title, row.SortKey, row.Released, row.Added, row.Art, row.Duration, row.Body, row.Slug)
		statements[i] = plainItemUpsert("sets", params)
	}
	return c.apply(ctx, statements)
}

// UpsertSeries writes series rows in place. The series table adds nfo_facts
// after the header, so this upsert names its own columns, as the movies one
// does.
func (c *Catalog) UpsertSeries(ctx context.Context, rows []seriesRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		params := itemParams(row.Library, row.Id, row.Kind, row.Path, row.Title, row.SortKey, row.Released, row.Added, row.Art, row.Duration, row.Body, row.Slug)
		statements[i] = statement{
			sql: `INSERT INTO series (library, id, kind, path, title, sort_key, released, added, art, duration, body, slug, nfo_facts, arts) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, id) DO UPDATE SET ` +
				`kind = excluded.kind, path = excluded.path, title = excluded.title, ` +
				`sort_key = excluded.sort_key, released = excluded.released, added = excluded.added, art = excluded.art, ` +
				`duration = excluded.duration, body = excluded.body, slug = excluded.slug, ` +
				`nfo_facts = excluded.nfo_facts, arts = excluded.arts`,
			params: append(params, row.NFOFacts, artsParam(row.Arts)),
		}
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
			sql: `INSERT INTO episodes (library, id, kind, path, title, sort_key, released, added, art, duration, body, slug, series, season, episode, arts) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, id) DO UPDATE SET ` +
				`kind = excluded.kind, path = excluded.path, title = excluded.title, ` +
				`sort_key = excluded.sort_key, released = excluded.released, added = excluded.added, art = excluded.art, ` +
				`duration = excluded.duration, body = excluded.body, slug = excluded.slug, ` +
				`series = excluded.series, season = excluded.season, episode = excluded.episode, arts = excluded.arts`,
			params: []any{row.Library, row.Id, row.Kind, row.Path, row.Title, row.SortKey, row.Released, row.Added, row.Art, row.Duration, string(payload), row.Slug, row.Series, row.Season, row.Episode, artsParam(row.Arts)},
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
			sql: `INSERT INTO files (library, path, container, video_codec, audio_codec, width, height, size_bytes, duration_ms, bitrate, probed, trickplay, present, type, role, language, modified, arrived) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, path) DO UPDATE SET ` +
				`container = excluded.container, video_codec = excluded.video_codec, ` +
				`audio_codec = excluded.audio_codec, width = excluded.width, height = excluded.height, ` +
				`size_bytes = excluded.size_bytes, duration_ms = excluded.duration_ms, ` +
				`bitrate = excluded.bitrate, probed = excluded.probed, ` +
				`trickplay = excluded.trickplay, present = excluded.present, ` +
				`type = excluded.type, role = excluded.role, language = excluded.language, modified = excluded.modified, arrived = excluded.arrived`,
			params: []any{row.Library, row.Path, row.Container, row.VideoCodec, row.AudioCodec, row.Width, row.Height, row.SizeBytes, row.DurationMs, row.Bitrate, row.Probed, row.Trickplay, present, row.Type, row.Role, row.Language, row.Modified, row.Arrived},
		}
	}
	return c.apply(ctx, statements)
}

// UpsertFileItems writes the many-to-many link, one row per (file, item) pair a
// file carries.
//
// Every column of file_items is a primary-key column, so the row
// carries nothing to update and the upsert does nothing on a conflict.
// A repeat write changes no row and broadcasts nothing.
func (c *Catalog) UpsertFileItems(ctx context.Context, rows []fileRow) (int, error) {
	var statements []statement
	for _, row := range rows {
		for _, item := range row.Items {
			statements = append(statements, statement{
				sql:    `INSERT INTO file_items (library, path, item) VALUES (?, ?, ?) ON CONFLICT (library, path, item) DO NOTHING`,
				params: []any{row.Library, row.Path, item},
			})
		}
	}
	return c.apply(ctx, statements)
}

// The write of one trailer in place. The provider and that provider's own key
// name the row beside the item, so a re-run updates the row and never adds a
// second one for the same video.
func (c *Catalog) UpsertTrailers(ctx context.Context, rows []trailerRow) (int, error) {
	statements := make([]statement, len(rows))
	for i, row := range rows {
		statements[i] = statement{
			sql: `INSERT INTO trailers (library, item, provider, key, site, url, name, kind, ` +
				`language, official, published, resolution, score, reason) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ` +
				`ON CONFLICT (library, item, provider, key) DO UPDATE SET ` +
				`site = excluded.site, url = excluded.url, name = excluded.name, ` +
				`kind = excluded.kind, language = excluded.language, ` +
				`official = excluded.official, published = excluded.published, ` +
				`resolution = excluded.resolution, ` +
				`score = excluded.score, reason = excluded.reason`,
			params: []any{row.Library, row.Item, row.Provider, row.Key, row.Site, row.URL,
				row.Name, row.Kind, row.Language, presentValue(row.Official), row.Published,
				row.Resolution, row.Score, row.Reason},
		}
	}
	return c.apply(ctx, statements)
}

// The trailers of one item as a set: the old rows leave and the new ones land
// in one apply. A provider that dropped a video would otherwise leave its row
// behind.
func (c *Catalog) ReplaceTrailers(ctx context.Context, library, item string, rows []trailerRow) (int, error) {
	statements := []statement{{
		sql:    `DELETE FROM trailers WHERE library = ? AND item = ?`,
		params: []any{library, item},
	}}
	for _, row := range rows {
		statements = append(statements, statement{
			sql: `INSERT INTO trailers (library, item, provider, key, site, url, name, kind, ` +
				`language, official, published, resolution, score, reason) ` +
				`VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			params: []any{row.Library, row.Item, row.Provider, row.Key, row.Site, row.URL,
				row.Name, row.Kind, row.Language, presentValue(row.Official), row.Published,
				row.Resolution, row.Score, row.Reason},
		})
	}
	return c.apply(ctx, statements)
}

// The delete names every key column, so a sweep removes the rows it read and
// nothing from another library.
func (c *Catalog) DeleteTrailers(ctx context.Context, library string, keys []trailerKey) (int, error) {
	statements := make([]statement, len(keys))
	for i, key := range keys {
		statements[i] = statement{
			sql:    `DELETE FROM trailers WHERE library = ? AND item = ? AND provider = ? AND key = ?`,
			params: []any{library, key.Item, key.Provider, key.Key},
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
			sql:    `INSERT INTO aliases (library, alias, item, source) VALUES (?, ?, ?, ?) ON CONFLICT (library, alias) DO UPDATE SET item = excluded.item, source = excluded.source`,
			params: []any{row.Library, row.Alias, row.Item, row.Source},
		}
	}
	return c.apply(ctx, statements)
}

// deleteByKey builds one delete per key against a two-column primary
// key, the library and the table's own key, so a delete reaches one
// library's row and never another library's row of the same name.
// table and column are constants this package names and never input.
func deleteByKey(table, column, library string, keys []string) []statement {
	statements := make([]statement, len(keys))
	for i, key := range keys {
		statements[i] = statement{
			sql:    `DELETE FROM ` + table + ` WHERE library = ? AND ` + column + ` = ?`,
			params: []any{library, key},
		}
	}
	return statements
}

// DeleteMovies removes movie rows whose titles left the volume.
func (c *Catalog) DeleteMovies(ctx context.Context, library string, ids []string) (int, error) {
	return c.apply(ctx, deleteByKey("movies", "id", library, ids))
}

// DeleteSets removes set rows whose last member left the library.
func (c *Catalog) DeleteSets(ctx context.Context, library string, ids []string) (int, error) {
	return c.apply(ctx, deleteByKey("sets", "id", library, ids))
}

// DeleteSeries removes series rows whose titles left the volume.
func (c *Catalog) DeleteSeries(ctx context.Context, library string, ids []string) (int, error) {
	return c.apply(ctx, deleteByKey("series", "id", library, ids))
}

// DeleteEpisodes removes episode rows whose files left the volume.
func (c *Catalog) DeleteEpisodes(ctx context.Context, library string, ids []string) (int, error) {
	return c.apply(ctx, deleteByKey("episodes", "id", library, ids))
}

// DeleteFiles removes file rows whose files left the volume.
func (c *Catalog) DeleteFiles(ctx context.Context, library string, paths []string) (int, error) {
	return c.apply(ctx, deleteByKey("files", "path", library, paths))
}

// DeleteAliases removes alias rows a re-walk no longer produces.
func (c *Catalog) DeleteAliases(ctx context.Context, library string, aliases []string) (int, error) {
	return c.apply(ctx, deleteByKey("aliases", "alias", library, aliases))
}

// The replicated tables of the schema: the set the read of every
// library the catalog holds covers. A table added to the schema is one
// entry here. The runs table is one of them, so a library that has lost
// every item row but whose last Job wrote a run is still a library the
// reporter reports on.
var catalogTables = []string{"aliases", "movies", "sets", "series", "episodes", "file_items", "files", "streams", "runs", "confirmations", "attempts",
	"contributors", "contributor_aliases", "credits", "genres", "trailers",
	"franchises", "franchise_members", "franchise_runs", "tallies"}

// DeleteFileItems names all three columns of the link row, because all
// three are the primary key. A delete by fewer would take every other
// link the library, the file, or the item holds with it.
func (c *Catalog) DeleteFileItems(ctx context.Context, library string, links []fileItemKey) (int, error) {
	statements := make([]statement, len(links))
	for i, link := range links {
		statements[i] = statement{
			sql:    `DELETE FROM file_items WHERE library = ? AND path = ? AND item = ?`,
			params: []any{library, link.Path, link.Item},
		}
	}
	return c.apply(ctx, statements)
}
