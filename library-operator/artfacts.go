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
// the episode's.
var artFactNames = []string{
	factPoster, factBackdrop, factLogo, factClearart, factBanner,
	factLandscape, factDiscart, factSeasonPoster, factSeasonBanner, factEpisodeThumb,
}

// The art facts the Library's own sources serve, in the order the group runs
// them. A Library whose sources hold one provider asks for what that provider
// serves and no more.
func servedArtFacts(library *Library, providers providerSet) []string {
	var served []string
	for _, fact := range artFactNames {
		if !artTypes[fact].holds(library.Spec.Kind) {
			continue
		}
		if providers.serving(library.Metadata.Namespace, library.Spec.Sources, fact) != nil {
			served = append(served, fact)
		}
	}
	return served
}

// One art type. The file is the name Kodi and Jellyfin read beside the title,
// from https://kodi.wiki/view/Artwork and
// https://jellyfin.org/docs/general/server/media/movies/, both read on
// 2026-09-03. For the two season facts the file is the last part of the name
// alone, because the season number leads it, and the episode thumbnail carries
// no file at all, because it is named for its episode file. The kind is the
// one kind of library that carries this art, and empty for the art both kinds
// carry. The list is the TMDb array the fact reads, and the size is the one it
// fetches.
type artType struct {
	fact string
	file string
	kind string
	list string
	size string
}

// Whether a library of this kind carries this art at all. A disc is a
// movie's, so a series is never a discart gap, and a series library never
// names the fact in its container.
func (t artType) holds(kind string) bool {
	return t.kind == "" || t.kind == kind
}

// The ten types, with the sizes this project fetches from TMDb. The five
// names this wave adds come from the Kodi forum's artwork naming thread at
// https://forum.kodi.tv/showthread.php?tid=248825 and from
// https://kodi.wiki/view/Artwork/Season, both read on 2026-09-03. These sizes
// and not the original, because the browser of plan 22 draws on a 1080p panel
// and holds every decoded image in memory, and a raster over 2 MiB draws in
// bands. The sizes are the open decision plan 30 lists under what is not
// decided.
var artTypes = map[string]artType{
	factPoster:       {fact: factPoster, file: "poster.jpg", list: tmdbPosters, size: "w780"},
	factBackdrop:     {fact: factBackdrop, file: "fanart.jpg", list: tmdbBackdrops, size: "w1280"},
	factLogo:         {fact: factLogo, file: "clearlogo.png", list: tmdbLogos, size: "w500"},
	factClearart:     {fact: factClearart, file: "clearart.png"},
	factBanner:       {fact: factBanner, file: "banner.jpg"},
	factLandscape:    {fact: factLandscape, file: "landscape.jpg"},
	factDiscart:      {fact: factDiscart, file: "disc.png", kind: libraryKindMovies},
	factSeasonPoster: {fact: factSeasonPoster, file: "poster.jpg", list: tmdbPosters, size: "w780"},
	factSeasonBanner: {fact: factSeasonBanner, file: "banner.jpg"},
	factEpisodeThumb: {fact: factEpisodeThumb, list: tmdbStills, size: "w300"},
}

// The start of the name Kodi reads for the art of season zero, which holds
// the specials.
const specialsSeasonPrefix = "season-specials-"

// The name of one season's art. Kodi reads it in the series folder beside
// tvshow.nfo, not in the season folder.
func seasonArtName(season int, suffix string) string {
	if season == 0 {
		return specialsSeasonPrefix + suffix
	}
	return fmt.Sprintf("season%02d-%s", season, suffix)
}

// The name of one episode's thumbnail: the episode file's own name with the
// extension replaced, which is what both players read.
func episodeThumbName(video string) string {
	base := filepath.Base(video)
	return strings.TrimSuffix(base, filepath.Ext(base)) + "-thumb.jpg"
}

// The file one gap writes, relative to the folder that holds it. The facts of
// a title key on the file name itself, the two season facts key on the season
// number, and the episode thumbnail keys on the episode file it goes beside.
func (t artType) fileFor(gap artGap) string {
	switch t.fact {
	case factSeasonPoster, factSeasonBanner:
		return seasonArtName(gap.season, t.file)
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

// The gap query of the title's own art, over the movies and the series of one
// library. A gap is a title with a TMDb id and no file of that name in the
// catalog, outside the retry window. The query asks for the id because a gap
// the enricher cannot close would schedule a Job every pass for ever, so a
// title no provider can name is no gap. The join onto aliases is what reads
// the TMDb id of a series or a movie whose own id is under another scheme.
func titleArtGapSQL(fact string) string {
	art := artTypes[fact]
	branch := func(table, scope string) string {
		return `SELECT t.library AS library, t.path || '/` + art.file + `' AS file, ` +
			`substr(a.alias, length('` + scope + `:tmdb:') + 1) AS tmdb ` +
			`FROM ` + table + ` AS t JOIN aliases AS a ` +
			`ON a.library = t.library AND a.item = t.id AND a.alias LIKE '` + scope + `:tmdb:%'`
	}
	// The branches are the kinds this art belongs to, so the disc art reads
	// the movies and never the series.
	branches := []string{}
	if art.holds(libraryKindMovies) {
		branches = append(branches, branch("movies", scopeMovie))
	}
	if art.holds(libraryKindSeries) {
		branches = append(branches, branch("series", scopeSeries))
	}
	return `SELECT file, tmdb, 0, 0 FROM (` + strings.Join(branches, ` UNION ALL `) +
		`) AS wanted WHERE library = ?1 AND ` + artFileClause() + ` AND ` + attemptClause(fact, "file")
}

// One row per season a library holds episodes of, because the catalog keeps
// no season item. The art lands in the series folder under Kodi's own name.
func seasonArtGapSQL(fact string) string {
	suffix := artTypes[fact].file
	return `SELECT file, tmdb, season, 0 FROM (` +
		`SELECT DISTINCT s.library AS library, ` +
		`s.path || '/' || CASE WHEN e.season = 0 THEN '` + specialsSeasonPrefix + suffix + `' ` +
		`ELSE printf('season%02d-` + suffix + `', e.season) END AS file, ` +
		`substr(a.alias, length('` + scopeSeries + `:tmdb:') + 1) AS tmdb, e.season AS season ` +
		`FROM episodes AS e ` +
		`JOIN series AS s ON s.library = e.library AND s.id = e.series ` +
		`JOIN aliases AS a ON a.library = s.library AND a.item = s.id ` +
		`AND a.alias LIKE '` + scopeSeries + `:tmdb:%'` +
		`) AS wanted WHERE library = ?1 AND ` + artFileClause() + ` AND ` +
		attemptClause(fact, "file")
}

// One row per episode with no image of its own. The episode keys on its own
// file, because the thumbnail is named for that file. The check reads the
// link table, so an image another tool named differently still counts as the
// episode's.
// The episodes that hold an image are one list built once from the library's
// files, never a subquery that reads the outer row, because that form walked
// every file of the library once per episode and took forty seconds.
func episodeThumbGapSQL() string {
	return `SELECT video, tmdb, season, episode FROM (` +
		`SELECT e.library AS library, e.path AS video, e.id AS item, ` +
		`substr(a.alias, length('` + scopeSeries + `:tmdb:') + 1) AS tmdb, ` +
		`e.season AS season, e.episode AS episode ` +
		`FROM episodes AS e ` +
		`JOIN series AS s ON s.library = e.library AND s.id = e.series ` +
		`JOIN aliases AS a ON a.library = s.library AND a.item = s.id ` +
		`AND a.alias LIKE '` + scopeSeries + `:tmdb:%'` +
		`) AS wanted WHERE library = ?1 ` +
		`AND item NOT IN (SELECT fi.item FROM file_items AS fi ` +
		`JOIN files AS f ON f.library = fi.library AND f.path = fi.path ` +
		`WHERE fi.library = ?1 ` +
		`AND f.type = '` + fileTypeImage + `' ` +
		`AND f.role IN ('` + fileRoleThumb + `', '` + fileRoleStill + `')) AND ` +
		attemptClause(factEpisodeThumb, "video")
}

// The file the fact would write is not in the catalog. This is the whole of
// "written where none exists" as the catalog can answer it, and the container
// checks the volume itself before it writes. The library is the first bound
// parameter by number, never the derived table's own column, because a
// subquery that reads the outer row is run again for every title, and this
// one over a library's files took seven seconds a fact.
func artFileClause() string {
	return `file NOT IN (SELECT path FROM files WHERE files.library = ?1)`
}
