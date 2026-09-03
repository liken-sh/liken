package main

// The art phase's own table: the local name each fact writes, the TMDb list
// it reads, the size it fetches, and the gap query that says which files the
// library has none of. The fact names are in factnames.go, and the maps in
// enrich.go, factsrole.go, and attempts.go name these facts as they name
// every other.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// The art facts this image can fill, in the order the art container names
// them in LIBRARY_FACTS: the title's own art first, then the season's, then
// the episode's. The other five art types are Fanart.tv's alone, and a later
// wave asks for them.
var artFactNames = []string{factPoster, factBackdrop, factLogo, factSeasonPoster, factEpisodeThumb}

// One art type. The file is the name Kodi and Jellyfin read beside the title,
// from https://kodi.wiki/view/Artwork and
// https://jellyfin.org/docs/general/server/media/movies/, both read on
// 2026-09-03. The season poster and the episode thumbnail carry no fixed
// name, because each is named for its season or its episode file, so their
// file is empty and the two functions below name them. The list is the TMDb
// array the fact reads, and the size is the one it fetches.
type artType struct {
	fact string
	file string
	list string
	size string
}

// The five types, with the sizes this project fetches. These sizes and not
// the original, because the browser of plan 22 draws on a 1080p panel and
// holds every decoded image in memory, and a raster over 2 MiB draws in
// bands. The sizes are the open decision plan 30 lists under what is not
// decided.
var artTypes = map[string]artType{
	factPoster:       {fact: factPoster, file: "poster.jpg", list: tmdbPosters, size: "w780"},
	factBackdrop:     {fact: factBackdrop, file: "fanart.jpg", list: tmdbBackdrops, size: "w1280"},
	factLogo:         {fact: factLogo, file: "clearlogo.png", list: tmdbLogos, size: "w500"},
	factSeasonPoster: {fact: factSeasonPoster, list: tmdbPosters, size: "w780"},
	factEpisodeThumb: {fact: factEpisodeThumb, list: tmdbStills, size: "w300"},
}

// The name Kodi reads for the poster of season zero, which holds the
// specials.
const specialsPosterName = "season-specials-poster.jpg"

// The name of one season's poster. Kodi reads it in the series folder beside
// tvshow.nfo, not in the season folder.
func seasonPosterName(season int) string {
	if season == 0 {
		return specialsPosterName
	}
	return fmt.Sprintf("season%02d-poster.jpg", season)
}

// The name of one episode's thumbnail: the episode file's own name with the
// extension replaced, which is what both players read.
func episodeThumbName(video string) string {
	base := filepath.Base(video)
	return strings.TrimSuffix(base, filepath.Ext(base)) + "-thumb.jpg"
}

// The file one gap writes, relative to the folder that holds it. Four of the
// facts key on the file itself, and the episode thumbnail keys on the episode
// file it goes beside.
func (t artType) fileFor(gap artGap) string {
	switch t.fact {
	case factSeasonPoster:
		return seasonPosterName(gap.season)
	case factEpisodeThumb:
		return episodeThumbName(gap.key)
	default:
		return t.file
	}
}

// How much memory the art container may take. It is above the scanner's
// because this container holds one image in memory while it writes it, where
// every other container holds one row at a time.
const artMemoryLimit = "256Mi"

// The name of the container that runs the art facts.
const artContainerName = "art"

// What the enricher records as the answer for a file another tool already
// wrote. The ledger says so, and the file is never opened.
const artProviderExisting = "existing"

// The language the choice prefers. A Library declares no language yet, so
// this is the one the project's own libraries hold. The field belongs on the
// Library.
const artLanguage = "en"

// One gap: the key the attempt is recorded under, the TMDb id of the title,
// and the season and episode numbers where the fact needs them. The key is
// the path of the file the fact writes, or the episode file the thumbnail
// goes beside, so a gap that is already filled leaves the list.
type artGap struct {
	key     string
	tmdb    string
	season  int
	episode int
}

// The folder the file lands in and the entry the ledger keys on. Both come
// off the key, so one rule places the file, the ledger, and the attempt.
func (g artGap) folder() string {
	return filepath.Dir(g.key)
}

func (g artGap) entry() string {
	return filepath.Base(g.key)
}

// One fact's run, bound to its name, so every art fact runs the same loop
// over its own gap.
func artFactRun(fact string) factRun {
	return func(ctx context.Context, e *enricher) error { return e.artFact(ctx, fact) }
}

// The provider one Library's sources hold for the art: the first of them that
// is Ready and serves any art fact.
func (s providerSet) servingArt(namespace string, sources []string) *MetadataProvider {
	for _, fact := range artFactNames {
		if provider := s.serving(namespace, sources, fact); provider != nil {
			return provider
		}
	}
	return nil
}

// The gap query of the title's own art, over the movies and the series of one
// library. A gap is a title with a TMDb id and no file of that name in the
// catalog, outside the retry window. The query asks for the id because a gap
// the enricher cannot close would schedule a Job every pass for ever, so a
// title no provider can name is no gap. The join onto aliases is what reads
// the TMDb id of a series or a movie whose own id is under another scheme.
func titleArtGapSQL(fact string) string {
	file := artTypes[fact].file
	branch := func(table, scope string) string {
		return `SELECT t.library AS library, t.path || '/` + file + `' AS file, ` +
			`substr(a.alias, length('` + scope + `:tmdb:') + 1) AS tmdb ` +
			`FROM ` + table + ` AS t JOIN aliases AS a ` +
			`ON a.library = t.library AND a.item = t.id AND a.alias LIKE '` + scope + `:tmdb:%'`
	}
	return `SELECT file, tmdb, 0, 0 FROM (` +
		branch("movies", scopeMovie) + ` UNION ALL ` + branch("series", scopeSeries) +
		`) AS wanted WHERE library = ? AND ` + artFileClause() + ` AND ` + artAttemptClause(fact, "file")
}

// One row per season a library holds episodes of, because the catalog keeps
// no season item. The poster lands in the series folder under Kodi's own
// name.
func seasonPosterGapSQL() string {
	return `SELECT file, tmdb, season, 0 FROM (` +
		`SELECT DISTINCT s.library AS library, ` +
		`s.path || '/' || CASE WHEN e.season = 0 THEN '` + specialsPosterName + `' ` +
		`ELSE printf('season%02d-poster.jpg', e.season) END AS file, ` +
		`substr(a.alias, length('` + scopeSeries + `:tmdb:') + 1) AS tmdb, e.season AS season ` +
		`FROM episodes AS e ` +
		`JOIN series AS s ON s.library = e.library AND s.id = e.series ` +
		`JOIN aliases AS a ON a.library = s.library AND a.item = s.id ` +
		`AND a.alias LIKE '` + scopeSeries + `:tmdb:%'` +
		`) AS wanted WHERE library = ? AND ` + artFileClause() + ` AND ` +
		artAttemptClause(factSeasonPoster, "file")
}

// One row per episode with no image of its own. The episode keys on its own
// file, because the thumbnail is named for that file. The check reads the
// link table, so an image another tool named differently still counts as the
// episode's.
func episodeThumbGapSQL() string {
	return `SELECT video, tmdb, season, episode FROM (` +
		`SELECT e.library AS library, e.path AS video, e.id AS item, ` +
		`substr(a.alias, length('` + scopeSeries + `:tmdb:') + 1) AS tmdb, ` +
		`e.season AS season, e.episode AS episode ` +
		`FROM episodes AS e ` +
		`JOIN series AS s ON s.library = e.library AND s.id = e.series ` +
		`JOIN aliases AS a ON a.library = s.library AND a.item = s.id ` +
		`AND a.alias LIKE '` + scopeSeries + `:tmdb:%'` +
		`) AS wanted WHERE library = ? ` +
		`AND NOT EXISTS (SELECT 1 FROM file_items AS fi ` +
		`JOIN files AS f ON f.library = fi.library AND f.path = fi.path ` +
		`WHERE fi.library = wanted.library AND fi.item = wanted.item ` +
		`AND f.type = '` + fileTypeImage + `' ` +
		`AND f.role IN ('` + fileRoleThumb + `', '` + fileRoleStill + `')) AND ` +
		artAttemptClause(factEpisodeThumb, "video")
}

// The file the fact would write is not in the catalog. This is the whole of
// "written where none exists" as the catalog can answer it, and the container
// checks the volume itself before it writes.
func artFileClause() string {
	return `file NOT IN (SELECT path FROM files WHERE files.library = wanted.library)`
}

// The attempt window, as every gap query carries it: an item tried inside the
// window is no gap, unless that attempt ended in an error.
func artAttemptClause(fact, column string) string {
	return column + ` NOT IN (SELECT item FROM attempts WHERE attempts.library = wanted.library ` +
		`AND ` + attemptFactColumn + ` = '` + fact + `' AND result != 'error' AND at >= ?)`
}
