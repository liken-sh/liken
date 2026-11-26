package main

// webhookbody.go builds the document a walk Job posts to its Library's
// webhooks (plans 99 and 100): every title whose videos the walk added or
// replaced, with the catalog's rows of the title keyed by column name, the
// change of each movie and episode, and the videos the walk took away from
// each one, as the catalog held them before the walk. The rows are the
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

// The changes a body reports of an item, a movie or an episode. An item is
// added when the catalog held no video of it before the walk. It is replaced
// when the walk took a video away from it, by a delete or by a new file at
// the same path, and a video arrived. It gained a version when it kept every
// video it had and another arrived.
const (
	itemAdded        = "added"
	itemReplaced     = "replaced"
	itemVersionAdded = "version-added"
)

// The change of a series that held a video before the walk. A series that
// held none is added.
const seriesChanged = "changed"

// One movie or one series of a body. Previous is a movie's own, and a series
// carries it on each episode, so a series has none. It is a pointer so a
// movie's empty list is in the document, and a series' is not.
type walkTitle struct {
	Change     string           `json:"change"`
	Table      string           `json:"table"`
	Row        map[string]any   `json:"row"`
	Files      []walkFile       `json:"files,omitempty"`
	Previous   *[]walkFile      `json:"previous,omitempty"`
	Episodes   []walkEpisode    `json:"episodes,omitempty"`
	Genres     []map[string]any `json:"genres"`
	Credits    []walkCredit     `json:"credits"`
	Trailers   []map[string]any `json:"trailers"`
	Set        map[string]any   `json:"set"`
	Franchises []walkFranchise  `json:"franchises"`
}

// One episode of a series whose videos the walk changed.
type walkEpisode struct {
	Change   string         `json:"change"`
	Row      map[string]any `json:"row"`
	Files    []walkFile     `json:"files"`
	Previous []walkFile     `json:"previous"`
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
	change walkChange) ([]walkTitle, error) {
	// Each title, and for a series the episodes whose videos changed.
	episodesOf := map[string][]string{}
	for _, path := range changedPaths(change.files) {
		for _, item := range change.after[path].Items {
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
		title, err := buildWalkTitle(ctx, catalog, library, kind, id, episodesOf[id], change)
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
	episodes []string, change walkChange) (walkTitle, error) {
	table := titleTable(kind)
	title := walkTitle{Table: table}
	var err error
	if title.Row, err = catalog.bodyRow(ctx, `SELECT * FROM `+table+` WHERE library = ? AND id = ?`, library, id); err != nil {
		return title, fmt.Errorf("reading a changed title: %w", err)
	}
	if title.Row == nil {
		return title, nil
	}

	if kind == libraryKindSeries {
		aliases, err := aliasesOf(ctx, catalog, library, id)
		if err != nil {
			return title, err
		}
		if title.Change, err = seriesChange(ctx, catalog, library, id, aliases, change); err != nil {
			return title, err
		}
		if title.Episodes, err = walkEpisodes(ctx, catalog, library, episodes, aliases, change); err != nil {
			return title, err
		}
	} else {
		aliases, err := aliasesOf(ctx, catalog, library, id)
		if err != nil {
			return title, err
		}
		changed, previous := change.itemChange(id, append(aliases, id))
		title.Change = changed
		files, err := change.previousFiles(previous)
		if err != nil {
			return title, err
		}
		title.Previous = &files
		if title.Files, err = itemFiles(ctx, catalog, library, id, change.files); err != nil {
			return title, err
		}
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

// Every alias of one item. An alias maps each provider id of a movie or a
// series, and its folder key, to the item's id now, so an id from before the
// walk that the walk replaced, such as a folder key that identity replaced
// with a provider id, is an alias of the item.
func aliasesOf(ctx context.Context, catalog *Catalog, library, item string) ([]string, error) {
	aliases, err := catalog.queryStrings(ctx,
		`SELECT alias FROM aliases WHERE library = ? AND item = ?`, []any{library, item})
	if err != nil {
		return nil, fmt.Errorf("reading the names of a changed title: %w", err)
	}
	return aliases, nil
}

// The ids an episode can have had before the walk. An episode has no aliases
// of its own: its id is built from its series' id, so an episode whose series
// changed its id in the walk had the id built from that older series id. So
// the names are the episode's id built from each alias of its series.
func episodeNames(id string, seriesAliases []string, season, episode int) []string {
	names := []string{id}
	for _, alias := range seriesAliases {
		names = append(names, episodeID(alias, season, episode))
	}
	return names
}

// The change of one item, and the paths of the videos the walk took away from
// it, from the ids the item can have had before the walk.
func (c walkChange) itemChange(item string, names []string) (string, []string) {
	now := map[string]bool{}
	for _, path := range c.afterItems[item] {
		now[path] = true
	}
	prior := c.priorVideos(names, slices.Collect(maps.Keys(now)))
	if len(prior) == 0 {
		return itemAdded, nil
	}
	var previous []string
	for _, path := range prior {
		if !now[path] || c.files[path] == videoReplaced {
			previous = append(previous, path)
		}
	}
	if len(previous) > 0 {
		return itemReplaced, previous
	}
	return itemVersionAdded, nil
}

// A series is added when the catalog held no video of any of its episodes
// before the walk, and changed when it held one. Its names are its own id and
// aliases, for a file linked to the series itself, and the names of each of
// its episodes.
func seriesChange(ctx context.Context, catalog *Catalog, library, id string, aliases []string,
	change walkChange) (string, error) {
	names := append([]string{id}, aliases...)
	if err := catalog.stream(ctx, `SELECT id, season, episode FROM episodes WHERE library = ? AND series = ?`,
		[]any{library, id}, func(cells []any) error {
			if len(cells) < 3 {
				return nil
			}
			episode, _ := cells[0].(string)
			names = append(names, episodeNames(episode, aliases, int(int64Cell(cells[1])), int(int64Cell(cells[2])))...)
			return nil
		}); err != nil {
		return "", fmt.Errorf("reading the episodes of a changed series: %w", err)
	}
	now, err := titleVideos(ctx, catalog, library, libraryKindSeries, id)
	if err != nil {
		return "", err
	}
	if len(change.priorVideos(names, now)) == 0 {
		return itemAdded, nil
	}
	return seriesChanged, nil
}

// The videos the catalog held of an item before the walk, in path order: each
// video of the note that held one of the item's names, and each video the item
// holds now that the note holds too.
func (c walkChange) priorVideos(names []string, now []string) []string {
	prior := map[string]bool{}
	for _, name := range names {
		for _, path := range c.beforeItems[name] {
			prior[path] = true
		}
	}
	for _, path := range now {
		if _, held := c.before[path]; held {
			prior[path] = true
		}
	}
	return slices.Sorted(maps.Keys(prior))
}

// The videos the walk took away from an item, with the rows the scan kept.
// The scan keeps whole rows only of the videos under the folders a folders
// walk names, up to its budget, so a video it kept none of carries the
// columns of the note.
func (c walkChange) previousFiles(paths []string) ([]walkFile, error) {
	files := []walkFile{}
	for _, path := range paths {
		if encoded, held := c.rows[path]; held {
			var file walkFile
			if err := json.Unmarshal(encoded, &file); err != nil {
				return nil, fmt.Errorf("reading the note %s: %w", rowsBeforeNote, err)
			}
			files = append(files, file)
			continue
		}
		state := c.before[path]
		files = append(files, walkFile{Row: map[string]any{
			"path": path, "size_bytes": state.Size, "modified": state.Modified,
		}})
	}
	return files, nil
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

// The episodes whose videos changed, in the order a series draws them, each
// with its change and the videos the walk took away from it.
func walkEpisodes(ctx context.Context, catalog *Catalog, library string, ids []string,
	seriesAliases []string, change walkChange) ([]walkEpisode, error) {
	episodes := []walkEpisode{}
	for _, id := range ids {
		row, err := catalog.bodyRow(ctx, `SELECT * FROM episodes WHERE library = ? AND id = ?`, library, id)
		if err != nil {
			return nil, fmt.Errorf("reading a changed episode: %w", err)
		}
		if row == nil {
			continue
		}
		episode := walkEpisode{Row: row}
		names := episodeNames(id, seriesAliases, int(int64Cell(row["season"])), int(int64Cell(row["episode"])))
		changed, previous := change.itemChange(id, names)
		episode.Change = changed
		if episode.Previous, err = change.previousFiles(previous); err != nil {
			return nil, err
		}
		if episode.Files, err = itemFiles(ctx, catalog, library, id, change.files); err != nil {
			return nil, err
		}
		episodes = append(episodes, episode)
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
