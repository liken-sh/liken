package main

// series.go reads one folder per series into a series item, an episode item
// per episode, and one file row per video file. A file named for two episodes
// is one file that both episode items link to.
//
// A season is a grouping the media browser
// draws from the episodes' season numbers, so the walk records a season on each
// episode and mints no season item.
//
// It reads every file the series folder, its season folders, and its extras
// folders hold. Each one links to the episode whose own name it starts with,
// and to the series where it matches no episode.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// walkSeries reads a whole series root into one walkResult by collecting the
// folder stream. The tests and a small library use this whole-root read. It
// keeps no arrival ledger, so a walk of a checked-in tree writes nothing into
// it.
func walkSeries(root, library string, ignore ignoreSet) *walkResult {
	scan := folderScan{root: root, library: library, kind: libraryKindSeries, ignore: ignore}
	return collectFolders(walkTree(context.Background(), root, seriesFolderRule(scan)))
}

// seriesFolderRule is what the pool in walk.go needs to walk a series volume.
// Every directory under the root is one series, so the rule answers yes to all
// of them and the walk descends no further. A series folder's own season
// folders are read by the folder scan, not by the pool.
func seriesFolderRule(scan folderScan) folderRule {
	return folderRule{
		isTitle: func(string) bool { return true },
		scan: func(dir string, result *walkResult) {
			scanSeriesFolder(scan, dir, result)
		},
		ignore: scan.ignore,
	}
}

// scanSeriesFolder reads one series folder into the result: the series item
// with its genre rows, and an episode item and a file for every episode under
// it. The identity comes from tvshow.nfo where the folder holds one, and from
// the folder name where it does not. The series' added is the earliest arrival
// among its episodes, and zero where it has none.
func scanSeriesFolder(scan folderScan, dir string, result *walkResult) {
	root, library, ignore := scan.root, scan.library, scan.ignore
	name := filepath.Base(dir)
	meta, identified, err := seriesIdentity(dir, name)
	// The same rule the movies walk follows: a folder whose .nfo file could
	// not be read has no identity this pass, so it writes no row.
	if err != nil {
		result.noteReadError(err)
		return
	}

	title := meta.Title
	if !identified {
		title = name
	}

	key := folderKey(name)
	seriesID := itemID(scopeSeries, meta.ProviderIDs, key)
	primaryArt, allArt, err := discoverArt(root, dir)
	result.noteReadError(err)

	body := meta.Body
	body.ProviderIDs = meta.ProviderIDs

	series := len(result.series)
	result.series = append(result.series, seriesRow{
		Id:       seriesID,
		Library:  library,
		Kind:     libraryKindSeries,
		Path:     relativePath(root, dir),
		Title:    title,
		SortKey:  sortKey(title),
		Slug:     slug(title, meta.Year),
		Released: meta.Released,
		Art:      primaryArt,
		Arts:     allArt,
		Body:     body,
		NFOFacts: meta.NFOFacts,
	})
	result.genres = append(result.genres, genreRows(library, seriesID, body.Genres)...)
	result.aliases = append(result.aliases, aliasRowsForItem(library, scopeSeries, meta.ProviderIDs, key, seriesID)...)
	// The season mappings are read before any ledger, because each ledger
	// lifts an attempt only while its season's mapping is still the one the
	// attempt asked under.
	seasons, err := seriesSeasonShows(dir, ignore)
	result.noteReadError(err)
	for _, season := range seasons {
		for _, reason := range season.skipped {
			result.reopened.skippedShows = append(result.reopened.skippedShows, skippedShow{
				path: filepath.Join(relativePath(root, dir), season.name, seasonNFOName), reason: reason})
		}
	}
	seriesShows, folderShowsByName := seriesFolderShows(seasons)
	alternates := readLikenDir(likenDir{root: root, dir: dir, library: library, item: seriesID,
		shows: seriesShows}, result)
	result.series[series].Body.AlternateTitles = alternatesOf(alternates, title)
	result.titles++
	if !identified {
		result.unidentified++
		result.unidentifiedNames = append(result.unidentifiedNames, relativePath(root, dir))
	}

	// The episodes are read first, so the file pass has the two things it
	// needs from them: the videos that already have a row, and the episode
	// each of a season folder's files belongs to.
	folders := newSeriesFolders()
	episodeFiles, err := collectEpisodeFiles(dir, ignore)
	result.noteReadError(err)
	arrivals := episodeArrivals(episodeFiles, result)
	ledgers := newProbeLedgers(libraryKindSeries)
	for _, episode := range episodeFiles {
		before := len(result.episodes)
		probes, err := ledgers.of(episode.dir)
		result.noteReadError(err)
		folders.note(episode, scanEpisode(root, library, seriesID, episode, arrivals[episode], probes, result))
		for _, row := range result.episodes[before:] {
			result.series[series].Added = earliestArrival(result.series[series].Added, row.Added)
		}
	}

	scanSeriesFiles(root, dir, library, seriesID, ignore, folders, ledgers, folderShowsByName, result)
}

// The arrival of every episode file, read from one ledger per folder that
// holds episodes: a season folder, or the series folder for a file kept
// there.
func episodeArrivals(episodes []episodeFile, result *walkResult) map[episodeFile]fileArrival {
	byDir := map[string][]string{}
	var dirs []string
	for _, episode := range episodes {
		if _, held := byDir[episode.dir]; !held {
			dirs = append(dirs, episode.dir)
		}
		byDir[episode.dir] = append(byDir[episode.dir], episode.file)
	}
	arrivals := map[episodeFile]fileArrival{}
	for _, dir := range dirs {
		held, err := folderArrivals(dir, byDir[dir])
		result.noteReadError(err)
		for file, at := range held {
			arrivals[episodeFile{dir: dir, file: file}] = at
		}
	}
	return arrivals
}

// seriesFolders is what one series' episode pass leaves for its file pass,
// one entry per directory that held an episode. The three maps travel
// together because the file pass reads all three for every directory.
type seriesFolders struct {
	// episodes answers the episode a file in the directory belongs to, keyed by
	// the episode file's name without its extension.
	episodes map[string]map[string][]string
	// videos is the episode files that already have a row from the episode
	// pass, so the file pass writes no second one.
	videos map[string]map[string]bool
	// items keys on the episode file's own name, which is the item's path
	// relative to the folder, the way a .liken entry names it.
	items map[string]map[string]string
}

func newSeriesFolders() seriesFolders {
	return seriesFolders{
		episodes: map[string]map[string][]string{},
		videos:   map[string]map[string]bool{},
		items:    map[string]map[string]string{},
	}
}

// note records one episode file under the directory that holds it. A file the
// scanner could not number reports no id and is left for the file pass.
func (f seriesFolders) note(episode episodeFile, episodeItemIDs []string) {
	if len(episodeItemIDs) == 0 {
		return
	}
	if f.videos[episode.dir] == nil {
		f.videos[episode.dir] = map[string]bool{}
		f.episodes[episode.dir] = map[string][]string{}
		f.items[episode.dir] = map[string]string{}
	}
	f.videos[episode.dir][episode.file] = true
	f.episodes[episode.dir][stripAnyExtension(episode.file)] = episodeItemIDs
	f.items[episode.dir][episode.file] = episodeItemIDs[0]
}

// Every folder this pass reads files from has its .liken lifted here, because
// the probe records an attempt beside every file it opens, an extras folder's
// among them.
func scanSeriesFiles(root, dir, library, seriesID string, ignore ignoreSet, folders seriesFolders,
	ledgers *probeLedgers, shows map[string]string, result *walkResult) {
	probes, err := ledgers.of(dir)
	result.noteReadError(err)
	rows, streams, subdirectories, err := folderFiles{
		root:       root,
		dir:        dir,
		library:    library,
		place:      filePlace{kind: libraryKindSeries},
		item:       constantItem(seriesID),
		held:       folders.videos[dir],
		probes:     probes,
		identities: result.fileIdentities(),
	}.read()
	result.noteReadError(err)
	result.files = append(result.files, rows...)
	result.streams = append(result.streams, streams...)

	for _, name := range subdirectories {
		if ignore.skips(name) {
			continue
		}
		child := filepath.Join(dir, name)
		place := filePlace{kind: libraryKindSeries}
		if extras := extrasFolderName(name); extras != "" {
			place.extras = extras
		} else {
			place.season = true
		}
		probes, err := ledgers.of(child)
		result.noteReadError(err)
		rows, streams, _, err := folderFiles{
			root:       root,
			dir:        child,
			library:    library,
			place:      place,
			item:       episodeItem(folders.episodes[child], seriesID),
			held:       folders.videos[child],
			probes:     probes,
			identities: result.fileIdentities(),
		}.read()
		result.noteReadError(err)
		result.files = append(result.files, rows...)
		result.streams = append(result.streams, streams...)
		readLikenDir(likenDir{
			root: root, dir: child, library: library,
			item: seriesID, items: folders.items[child],
			shows: folderShows{folder: shows[name]},
		}, result)
	}
}

// seriesIdentity reads a series folder's identity, the same ladder the movies
// walk uses: a readable tvshow.nfo with a title, or the folder name,
// identified when the name yields a year or a provider id. The .nfo read
// answers the way movieIdentity's does: an absent .nfo file falls through to
// the name, and an .nfo file the scanner cannot read is an error.
func seriesIdentity(dir, name string) (seriesMeta, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, "tvshow.nfo"))
	switch {
	case err == nil:
		if meta, err := parseSeriesNFO(data); err == nil && meta.Title != "" {
			meta.ProviderIDs = mergeProviderIDs(meta.ProviderIDs, parseProviderIDs(name))
			return meta, true, nil
		}
	case !errors.Is(err, fs.ErrNotExist):
		return seriesMeta{}, false, err
	}
	title, year := parseReleaseName(name)
	if title == "" {
		title = name
	}
	ids := parseProviderIDs(name)
	return seriesMeta{
		Title: title, Year: year, Released: releasedFromYear(year), ProviderIDs: ids,
	}, year > 0 || len(ids) > 0, nil
}

// episodeFile is one episode file and the directory that holds it, so the
// season folder's name is at hand where the .nfo file held no season number.
type episodeFile struct {
	dir  string
	file string
}

// collectEpisodeFiles reads a series folder's episode files: the files in a
// season folder one level down, and any file directly in the series folder. The
// walk goes one level deep, because a season folder is the only nesting a series
// volume uses.
func collectEpisodeFiles(seriesDir string, ignore ignoreSet) ([]episodeFile, error) {
	var files []episodeFile
	videos, err := listVideoFiles(seriesDir)
	if err != nil {
		return files, err
	}
	for _, file := range videos {
		files = append(files, episodeFile{dir: seriesDir, file: file})
	}
	entries, err := os.ReadDir(seriesDir)
	if err != nil {
		return files, err
	}
	for _, entry := range entries {
		// The season descent skips the same two lists the pool's
		// descent skips: skipName, the closed list of dot-names and service
		// directories that are no season anywhere, and the ignore set this
		// Library declares. Neither one is read, so neither one marks the
		// pass incomplete.
		if !entry.IsDir() || skipName(entry.Name()) || ignore.skips(entry.Name()) {
			continue
		}
		seasonDir := filepath.Join(seriesDir, entry.Name())
		videos, err := listVideoFiles(seasonDir)
		if err != nil {
			return files, err
		}
		// A folder with an extras name that holds videos of its own is an
		// extras folder and no season, so a trailer that carries an episode
		// marker in its name is a file of the series and never an episode of
		// it. The test reads the videos this pass already listed, so the
		// folder costs one directory read. The file pass reads that folder,
		// one level deep, and rows what it holds.
		if len(videos) > 0 && extrasFolderName(entry.Name()) != "" {
			continue
		}
		for _, file := range videos {
			files = append(files, episodeFile{dir: seasonDir, file: file})
		}
	}
	return files, nil
}

// scanEpisode reads one episode file into an item per episode it holds and one
// file row for the file itself, and reports the item ids. The file is one file,
// and its path is the primary key of the files table, so a double episode is
// two items and one row.
//
// Nothing on the volume says where the second episode of a double begins, so
// the episodes split the file evenly. The link from the file to each episode
// records the episode's place in the order of the name's range marker and how
// many episodes the file holds, and a read splits a position in the file
// with those two numbers. An episode the scanner leaves out for having no
// number still counts, because its seconds are still in the file. When the
// file's duration is known, each episode's runtime is its share of that
// duration, so the runtime under a still and the length of a mark are one
// episode's length. A file with no duration keeps the runtime of each .nfo
// block.
//
// The season and episode numbers come from the
// episode .nfo beside the file where there is one, and from the season folder and
// the file name where none does. An episode the scanner cannot number is left
// out and reports no id, because it has no place under the series.
func scanEpisode(root, library, seriesID string, episode episodeFile, arrival fileArrival, probes folderProbes, result *walkResult) []string {
	metas, err := episodeIdentity(episode)
	// An episode whose .nfo file could not be read has no numbers this
	// pass, and a row read from the file name alone could carry another
	// episode's id.
	if err != nil {
		result.noteReadError(err)
		return nil
	}
	absolute := filepath.Join(episode.dir, episode.file)
	thumb, err := episodeThumb(root, episode.dir, episode.file)
	result.noteReadError(err)

	var episodeItemIDs []string
	var links []fileItem
	var episodes []episodeRow
	for index, meta := range metas {
		if meta.Episode <= 0 {
			continue
		}
		episodeItemID := episodeID(seriesID, meta.Season, meta.Episode)
		title := meta.Title
		if title == "" {
			title = fmt.Sprintf("S%02dE%02d", meta.Season, meta.Episode)
		}

		body := meta.Body
		var arts []string
		if thumb != "" {
			arts = []string{thumb}
		}

		episodes = append(episodes, episodeRow{
			Id:       episodeItemID,
			Library:  library,
			Kind:     libraryKindSeries,
			Path:     relativePath(root, absolute),
			Title:    title,
			SortKey:  sortKey(title),
			Slug:     slug(title, 0),
			Released: meta.Released,
			Added:    arrival.added,
			Art:      thumb,
			Arts:     arts,
			Duration: meta.Duration,
			Body:     body,
			Series:   seriesID,
			Season:   meta.Season,
			Episode:  meta.Episode,
			NFOFacts: meta.NFOFacts,
		})
		result.aliases = append(result.aliases, aliasRowsForItem(library, scopeEpisode, meta.ProviderIDs, "", episodeItemID)...)
		episodeItemIDs = append(episodeItemIDs, episodeItemID)
		links = append(links, fileItem{Item: episodeItemID, Part: index + 1, Parts: len(metas)})
	}
	if len(episodeItemIDs) == 0 {
		return nil
	}

	var stream *streamInfo
	if metas[0].Stream.present() {
		stream = &metas[0].Stream
	}
	container, videoCodec, audioCodec, width, height, durationMs := fileAttributes(episode.file, stream)
	size, modified, err := statFile(absolute)
	if err != nil {
		result.noteReadError(err)
		result.episodes = append(result.episodes, episodes...)
		return episodeItemIDs
	}
	class := classifyFile(episode.file, filePlace{kind: libraryKindSeries, season: true})
	row := fileRow{
		Path:       relativePath(root, absolute),
		Library:    library,
		Container:  container,
		VideoCodec: videoCodec,
		AudioCodec: audioCodec,
		Width:      width,
		Height:     height,
		SizeBytes:  size,
		DurationMs: durationMs,
		Trickplay:  trickplayFor(root, episode.dir, episode.file),
		Present:    true,
		Type:       class.Type,
		Role:       class.Role,
		Modified:   modified,
		Arrived:    arrival.arrived,
		Items:      links,
	}
	result.streams = append(result.streams, probes.fill(&row, episode.file, absolute, result.fileIdentities())...)
	result.files = append(result.files, row)
	if row.DurationMs > 0 {
		for i := range episodes {
			episodes[i].Duration = row.DurationMs / 1000 / int64(len(metas))
		}
	}
	result.episodes = append(result.episodes, episodes...)
	return episodeItemIDs
}
