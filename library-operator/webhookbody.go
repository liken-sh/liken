package main

// webhookbody.go builds the document a walk Job posts to its Library's
// webhooks (plan 99): every title whose videos the walk added or replaced,
// with the catalog's rows of the title keyed by column name. The rows are the
// catalog's own columns, so a column the schema gains reaches a receiver with
// no change here. Corrosion refuses to drop or rename a column, so a receiver
// that reads a column keeps reading it.
//
// The close container reads the rows after the hand-off, so the ids are the
// ones identity wrote and every phase's rows are in the document.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"
)

// The document one walk posts.
type walkBody struct {
	Namespace string      `json:"namespace"`
	Library   string      `json:"library"`
	Kind      string      `json:"kind"`
	Job       string      `json:"job"`
	Walk      string      `json:"walk"`
	Started   time.Time   `json:"started"`
	Finished  time.Time   `json:"finished"`
	Titles    []walkTitle `json:"titles"`
}

// The two walks a body names: the whole library, or the folders that
// webhooks named.
const (
	walkFull    = "full"
	walkFolders = "folders"
)

// The two changes a body reports of a title. A title is added when the
// catalog held no video of it before the walk.
const (
	titleAdded   = "added"
	titleChanged = "changed"
)

// One movie or one series of a body.
type walkTitle struct {
	Change     string           `json:"change"`
	Table      string           `json:"table"`
	Row        map[string]any   `json:"row"`
	Files      []walkFile       `json:"files,omitempty"`
	Episodes   []walkEpisode    `json:"episodes,omitempty"`
	Genres     []map[string]any `json:"genres"`
	Credits    []walkCredit     `json:"credits"`
	Trailers   []map[string]any `json:"trailers"`
	Set        map[string]any   `json:"set"`
	Franchises []walkFranchise  `json:"franchises"`
}

// One episode of a series whose videos the walk changed.
type walkEpisode struct {
	Row   map[string]any `json:"row"`
	Files []walkFile     `json:"files"`
}

// One file of a movie or an episode, with the rows that belong to it. Change
// is empty for a file that the walk did not change, such as a second
// encoding.
type walkFile struct {
	Change  string           `json:"change,omitempty"`
	Row     map[string]any   `json:"row"`
	Streams []map[string]any `json:"streams"`
	Marks   []map[string]any `json:"marks"`
}

// One credit, and the row of the person it names, which is null for a person
// the store has no entry for.
type walkCredit struct {
	Row         map[string]any `json:"row"`
	Contributor map[string]any `json:"contributor"`
}

// One franchise member row that names the title, with the franchise's row.
type walkFranchise struct {
	Member    map[string]any `json:"member"`
	Franchise map[string]any `json:"franchise"`
}

// The columns that hold JSON as text. A body carries their value as JSON, so
// a receiver reads an object and not a string it must parse again.
var jsonColumns = map[string]bool{"body": true, "arts": true, "universes": true, "dialogue": true}

// The rows of a query, with each JSON column decoded. A JSON column that does
// not parse, such as an empty dialogue, stays the text it is.
func (c *Catalog) bodyRows(ctx context.Context, sql string, params ...any) ([]map[string]any, error) {
	rows, err := c.namedRows(ctx, sql, params)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		for column := range jsonColumns {
			text, isText := row[column].(string)
			if !isText || text == "" {
				continue
			}
			var value any
			if json.Unmarshal([]byte(text), &value) == nil {
				row[column] = value
			}
		}
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return rows, nil
}

// The first row of a query, and nil where it returns none.
func (c *Catalog) bodyRow(ctx context.Context, sql string, params ...any) (map[string]any, error) {
	rows, err := c.bodyRows(ctx, sql, params...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

// The titles of the changed videos of one library, in the order of their ids.
func walkTitles(ctx context.Context, catalog *Catalog, library, kind string,
	changes map[string]string) ([]walkTitle, error) {
	// Each title, and for a series the episodes whose videos changed.
	episodesOf := map[string][]string{}
	for _, path := range changedPaths(changes) {
		items, err := catalog.queryStrings(ctx,
			`SELECT item FROM file_items WHERE library = ? AND path = ? ORDER BY item`, []any{library, path})
		if err != nil {
			return nil, fmt.Errorf("reading the items of a changed file: %w", err)
		}
		for _, item := range items {
			title, episode, err := titleOfItem(ctx, catalog, library, kind, item)
			if err != nil {
				return nil, err
			}
			if title == "" {
				continue
			}
			if episode != "" && !slices.Contains(episodesOf[title], episode) {
				episodesOf[title] = append(episodesOf[title], episode)
				continue
			}
			if _, held := episodesOf[title]; !held {
				episodesOf[title] = nil
			}
		}
	}

	titles := []walkTitle{}
	for _, id := range slices.Sorted(maps.Keys(episodesOf)) {
		title, err := buildWalkTitle(ctx, catalog, library, kind, id, episodesOf[id], changes)
		if err != nil {
			return nil, err
		}
		if title.Row != nil {
			titles = append(titles, title)
		}
	}
	return titles, nil
}

// The title an item belongs to: the movie itself, or an episode's series with
// the episode. An item of a series library that is not an episode, such as a
// file linked to the series, is the series.
func titleOfItem(ctx context.Context, catalog *Catalog, library, kind, item string) (string, string, error) {
	if kind != libraryKindSeries {
		return item, "", nil
	}
	series, err := catalog.queryStrings(ctx,
		`SELECT series FROM episodes WHERE library = ? AND id = ?`, []any{library, item})
	if err != nil {
		return "", "", fmt.Errorf("reading the series of a changed episode: %w", err)
	}
	if len(series) == 0 {
		return item, "", nil
	}
	return series[0], item, nil
}

// The table of a library's titles.
func titleTable(kind string) string {
	if kind == libraryKindSeries {
		return "series"
	}
	return "movies"
}

// One title of a body, read from the catalog.
func buildWalkTitle(ctx context.Context, catalog *Catalog, library, kind, id string,
	episodes []string, changes map[string]string) (walkTitle, error) {
	table := titleTable(kind)
	title := walkTitle{Table: table}
	var err error
	if title.Row, err = catalog.bodyRow(ctx, `SELECT * FROM `+table+` WHERE library = ? AND id = ?`, library, id); err != nil {
		return title, fmt.Errorf("reading a changed title: %w", err)
	}
	if title.Row == nil {
		return title, nil
	}

	videos, err := titleVideos(ctx, catalog, library, kind, id)
	if err != nil {
		return title, err
	}
	title.Change = titleChange(videos, changes)

	if kind == libraryKindSeries {
		if title.Episodes, err = walkEpisodes(ctx, catalog, library, episodes, changes); err != nil {
			return title, err
		}
	} else if title.Files, err = itemFiles(ctx, catalog, library, id, changes); err != nil {
		return title, err
	}

	if title.Genres, err = catalog.bodyRows(ctx,
		`SELECT * FROM genres WHERE library = ? AND item = ? ORDER BY rank`, library, id); err != nil {
		return title, fmt.Errorf("reading the genres of a changed title: %w", err)
	}
	if title.Trailers, err = catalog.bodyRows(ctx,
		`SELECT * FROM trailers WHERE library = ? AND item = ? ORDER BY provider, key`, library, id); err != nil {
		return title, fmt.Errorf("reading the trailers of a changed title: %w", err)
	}
	if title.Credits, err = creditsOfTitle(ctx, catalog, library, id); err != nil {
		return title, err
	}
	if set, _ := title.Row["set_id"].(string); set != "" {
		if title.Set, err = catalog.bodyRow(ctx,
			`SELECT * FROM sets WHERE library = ? AND id = ?`, library, set); err != nil {
			return title, fmt.Errorf("reading the set of a changed title: %w", err)
		}
	}
	if title.Franchises, err = franchisesOfTitle(ctx, catalog, library, id); err != nil {
		return title, err
	}
	return title, nil
}

// A title is added when every video it has now is a video the walk added, so
// the catalog held none of them before the walk.
func titleChange(videos []string, changes map[string]string) string {
	for _, path := range videos {
		if changes[path] != videoAdded {
			return titleChanged
		}
	}
	return titleAdded
}

// The paths of every video of one title: a movie's own, or every episode's of
// a series.
func titleVideos(ctx context.Context, catalog *Catalog, library, kind, id string) ([]string, error) {
	query := `SELECT f.path FROM files f JOIN file_items i ON i.library = f.library AND i.path = f.path ` +
		`WHERE f.library = ? AND f.type = ? AND i.item = ?`
	if kind == libraryKindSeries {
		query = `SELECT f.path FROM files f JOIN file_items i ON i.library = f.library AND i.path = f.path ` +
			`JOIN episodes e ON e.library = i.library AND e.id = i.item ` +
			`WHERE f.library = ? AND f.type = ? AND e.series = ?`
	}
	paths, err := catalog.queryStrings(ctx, query, []any{library, fileTypeVideo, id})
	if err != nil {
		return nil, fmt.Errorf("reading the videos of a changed title: %w", err)
	}
	return paths, nil
}

// The episodes whose videos changed, in the order a series draws them.
func walkEpisodes(ctx context.Context, catalog *Catalog, library string, ids []string,
	changes map[string]string) ([]walkEpisode, error) {
	episodes := []walkEpisode{}
	for _, id := range ids {
		row, err := catalog.bodyRow(ctx, `SELECT * FROM episodes WHERE library = ? AND id = ?`, library, id)
		if err != nil {
			return nil, fmt.Errorf("reading a changed episode: %w", err)
		}
		if row == nil {
			continue
		}
		files, err := itemFiles(ctx, catalog, library, id, changes)
		if err != nil {
			return nil, err
		}
		episodes = append(episodes, walkEpisode{Row: row, Files: files})
	}
	slices.SortStableFunc(episodes, func(a, b walkEpisode) int {
		if order := int64Cell(a.Row["season"]) - int64Cell(b.Row["season"]); order != 0 {
			return int(order)
		}
		return int(int64Cell(a.Row["episode"]) - int64Cell(b.Row["episode"]))
	})
	return episodes, nil
}

// Every file linked to one item, with its streams and its marks.
func itemFiles(ctx context.Context, catalog *Catalog, library, item string,
	changes map[string]string) ([]walkFile, error) {
	rows, err := catalog.bodyRows(ctx,
		`SELECT f.* FROM files f JOIN file_items i ON i.library = f.library AND i.path = f.path `+
			`WHERE f.library = ? AND i.item = ? ORDER BY f.path`, library, item)
	if err != nil {
		return nil, fmt.Errorf("reading the files of a changed title: %w", err)
	}
	files := make([]walkFile, 0, len(rows))
	for _, row := range rows {
		path, _ := row["path"].(string)
		file := walkFile{Change: changes[path], Row: row}
		if file.Streams, err = catalog.bodyRows(ctx,
			`SELECT * FROM streams WHERE library = ? AND path = ? ORDER BY ordinal`, library, path); err != nil {
			return nil, fmt.Errorf("reading the streams of a changed file: %w", err)
		}
		if file.Marks, err = catalog.bodyRows(ctx,
			`SELECT * FROM marks WHERE library = ? AND path = ? ORDER BY ordinal`, library, path); err != nil {
			return nil, fmt.Errorf("reading the marks of a changed file: %w", err)
		}
		files = append(files, file)
	}
	return files, nil
}

// The credits of one title in billing order, each with its person's row.
func creditsOfTitle(ctx context.Context, catalog *Catalog, library, item string) ([]walkCredit, error) {
	rows, err := catalog.bodyRows(ctx,
		`SELECT * FROM credits WHERE library = ? AND item = ? ORDER BY billing`, library, item)
	if err != nil {
		return nil, fmt.Errorf("reading the credits of a changed title: %w", err)
	}
	credits := make([]walkCredit, 0, len(rows))
	for _, row := range rows {
		credit := walkCredit{Row: row}
		if person, _ := row["contributor"].(string); person != "" {
			if credit.Contributor, err = catalog.bodyRow(ctx,
				`SELECT * FROM contributors WHERE library = ? AND path = ?`, library, person); err != nil {
				return nil, fmt.Errorf("reading a person of a changed title: %w", err)
			}
		}
		credits = append(credits, credit)
	}
	return credits, nil
}

// The franchise members that name one of the title's aliases, across every
// library of the namespace, each with its franchise's row.
func franchisesOfTitle(ctx context.Context, catalog *Catalog, library, item string) ([]walkFranchise, error) {
	members, err := catalog.bodyRows(ctx,
		`SELECT m.* FROM franchise_members m JOIN aliases a ON a.alias = m.alias `+
			`WHERE a.library = ? AND a.item = ? ORDER BY m.library, m.franchise, m.position`, library, item)
	if err != nil {
		return nil, fmt.Errorf("reading the franchises of a changed title: %w", err)
	}
	franchises := make([]walkFranchise, 0, len(members))
	for _, member := range members {
		owner, _ := member["library"].(string)
		id, _ := member["franchise"].(string)
		row, err := catalog.bodyRow(ctx, `SELECT * FROM franchises WHERE library = ? AND id = ?`, owner, id)
		if err != nil {
			return nil, fmt.Errorf("reading a franchise of a changed title: %w", err)
		}
		franchises = append(franchises, walkFranchise{Member: member, Franchise: row})
	}
	return franchises, nil
}
