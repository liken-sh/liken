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
// folder stream. The tests and a small library use this whole-root read.
func walkSeries(root, library string, ignore ignoreSet) *walkResult {
	return collectFolders(walkTree(context.Background(), root, seriesFolderRule(root, library, ignore)))
}

// seriesFolderRule is what the pool in walk.go needs to walk a series volume.
// Every directory under the root is one series, so the rule answers yes to all
// of them and the walk descends no further. A series folder's own season
// folders are read by the folder scan, not by the pool.
func seriesFolderRule(root, library string, ignore ignoreSet) folderRule {
	return folderRule{
		isTitle: func(string) bool { return true },
		scan: func(dir string, result *walkResult) {
			scanSeriesFolder(root, dir, library, ignore, result)
		},
		ignore: ignore,
	}
}

// scanSeriesFolder reads one series folder into the result: the series item,
// and an episode item and a file for every episode under it. The identity comes
// from tvshow.nfo where the folder holds one, and from the folder name where it
// does not.
func scanSeriesFolder(root, dir, library string, ignore ignoreSet, result *walkResult) {
	name := filepath.Base(dir)
	meta, identified, err := seriesIdentity(dir, name)
	// The same rule the movies walk follows: a folder whose sidecar could
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
	body.Art = allArt

	result.series = append(result.series, seriesRow{
		Id:       seriesID,
		Library:  library,
		Kind:     libraryKindSeries,
		Path:     relativePath(root, dir),
		Title:    title,
		SortKey:  sortKey(title),
		Slug:     slug(title, meta.Year),
		Released: meta.Released,
		Added:    addedTime(dir),
		Art:      primaryArt,
		Body:     body,
	})
	result.aliases = append(result.aliases, aliasRowsForItem(scopeSeries, meta.ProviderIDs, key, seriesID)...)
	result.titles++
	if !identified {
		result.unidentified++
		result.unidentifiedNames = append(result.unidentifiedNames, relativePath(root, dir))
	}

	// The episodes are read first, so the file pass has the two things it
	// needs from them: the videos that already have a row, and the episode
	// each of a season folder's files belongs to.
	episodesByDirectory := map[string]map[string][]string{}
	videosByDirectory := map[string]map[string]bool{}
	episodeFiles, err := collectEpisodeFiles(dir, ignore)
	result.noteReadError(err)
	for _, episode := range episodeFiles {
		episodeItemIDs := scanEpisode(root, library, seriesID, episode, result)
		if len(episodeItemIDs) == 0 {
			continue
		}
		if videosByDirectory[episode.dir] == nil {
			videosByDirectory[episode.dir] = map[string]bool{}
			episodesByDirectory[episode.dir] = map[string][]string{}
		}
		videosByDirectory[episode.dir][episode.file] = true
		episodesByDirectory[episode.dir][stripAnyExtension(episode.file)] = episodeItemIDs
	}

	scanSeriesFiles(root, dir, library, seriesID, ignore, episodesByDirectory, videosByDirectory, result)
}

// scanSeriesFiles reads the rest of a series folder: the tvshow.nfo and the art
// beside it, then one level down for a season folder's own art, sidecars, and
// subtitles, and for an extras folder's trailers and featurettes. A file
// directly in the series folder links to the series. A file in a season folder
// links to the episode whose own name it starts with, and to the series where
// it matches no episode, which is where a season poster lands.
func scanSeriesFiles(root, dir, library, seriesID string, ignore ignoreSet, episodes map[string]map[string][]string, videos map[string]map[string]bool, result *walkResult) {
	rows, subdirectories, err := folderFiles{
		root:    root,
		dir:     dir,
		library: library,
		place:   filePlace{kind: libraryKindSeries},
		item:    constantItem(seriesID),
		held:    videos[dir],
	}.read()
	result.noteReadError(err)
	result.files = append(result.files, rows...)

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
		rows, _, err := folderFiles{
			root:    root,
			dir:     child,
			library: library,
			place:   place,
			item:    episodeItem(episodes[child], seriesID),
			held:    videos[child],
		}.read()
		result.noteReadError(err)
		result.files = append(result.files, rows...)
	}
}

// seriesIdentity reads a series folder's identity, the same ladder the movies
// walk uses: a readable tvshow.nfo with a title, or the folder name, identified
// when the name yields a year.
//
// The sidecar read answers the way movieIdentity's does: an absent
// sidecar falls through to the name, and a sidecar the scanner cannot
// read is an error.
func seriesIdentity(dir, name string) (seriesMeta, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, "tvshow.nfo"))
	switch {
	case err == nil:
		if meta, err := parseSeriesNFO(data); err == nil && meta.Title != "" {
			return meta, true, nil
		}
	case !errors.Is(err, fs.ErrNotExist):
		return seriesMeta{}, false, err
	}
	title, year := parseReleaseName(name)
	if title == "" {
		title = name
	}
	return seriesMeta{Title: title, Year: year, Released: releasedFromYear(year)}, year > 0, nil
}

// episodeFile is one episode file and the directory that holds it, so the
// season folder's name is at hand where the sidecar carried no season number.
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
		if !entry.IsDir() || ignore.skips(entry.Name()) {
			continue
		}
		seasonDir := filepath.Join(seriesDir, entry.Name())
		videos, err := listVideoFiles(seasonDir)
		if err != nil {
			return files, err
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
// two items and one row. Both items play the file from the start, because
// nothing on the volume says where the second episode begins.
//
// The season and episode numbers come from the
// episode .nfo beside the file where there is one, and from the season folder and
// the file name where none does. An episode the scanner cannot number is left
// out and reports no id, because it has no place under the series.
func scanEpisode(root, library, seriesID string, episode episodeFile, result *walkResult) []string {
	metas, err := episodeIdentity(episode)
	// An episode whose sidecar could not be read has no numbers this
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
	for _, meta := range metas {
		if meta.Episode <= 0 {
			continue
		}
		episodeItemID := episodeID(seriesID, meta.Season, meta.Episode)
		title := meta.Title
		if title == "" {
			title = fmt.Sprintf("S%02dE%02d", meta.Season, meta.Episode)
		}

		body := meta.Body
		if thumb != "" {
			body.Art = []string{thumb}
		}

		result.episodes = append(result.episodes, episodeRow{
			Id:       episodeItemID,
			Library:  library,
			Kind:     libraryKindSeries,
			Path:     relativePath(root, absolute),
			Title:    title,
			SortKey:  sortKey(title),
			Slug:     slug(title, 0),
			Released: meta.Released,
			Added:    addedTime(absolute),
			Art:      thumb,
			Duration: meta.Duration,
			Body:     body,
			Series:   seriesID,
			Season:   meta.Season,
			Episode:  meta.Episode,
		})
		result.aliases = append(result.aliases, aliasRowsForItem(scopeEpisode, meta.ProviderIDs, "", episodeItemID)...)
		episodeItemIDs = append(episodeItemIDs, episodeItemID)
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
		return episodeItemIDs
	}
	class := classifyFile(episode.file, filePlace{kind: libraryKindSeries, season: true})
	result.files = append(result.files, fileRow{
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
		Items:      episodeItemIDs,
	})
	return episodeItemIDs
}

// episodeIdentity reads the numbers and the body of every episode one file
// holds. A range marker in the name numbers each of them, within one season.
//
// A sidecar of several episodedetails blocks gives each episode its own title
// and body, in the sidecar's own order. An episode past the last block takes
// the file's technical attributes and no title of its own, so scanEpisode
// names it S04E11 and no episode ever carries another episode's title.
//
// The .nfo beside the file
// is the source where one exists; the season folder and the file name fill a
// number the sidecar left at zero, so a sidecar that names only the episode
// still takes its season from the folder.
func episodeIdentity(episode episodeFile) ([]episodeMeta, error) {
	var blocks []episodeMeta
	nfoPath := filepath.Join(episode.dir, stripExtension(episode.file)+".nfo")
	data, err := os.ReadFile(nfoPath)
	switch {
	case err == nil:
		if parsed, err := parseEpisodeNFOs(data); err == nil {
			blocks = parsed
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}

	var first episodeMeta
	if len(blocks) > 0 {
		first = blocks[0]
	}
	season, episodeNumbers, marked := parseEpisodeMarker(episode.file)
	if first.Season == 0 {
		if folderSeason, ok := parseSeasonFolder(filepath.Base(episode.dir)); ok {
			first.Season = folderSeason
		} else if marked {
			first.Season = season
		}
	}
	if first.Episode == 0 && marked {
		first.Episode = episodeNumbers[0]
	}

	metas := []episodeMeta{first}
	for index := 1; index < len(episodeNumbers); index++ {
		next := episodeMeta{Stream: first.Stream, Duration: itemDuration(first.Stream, 0)}
		if index < len(blocks) {
			next = blocks[index]
		}
		next.Season = first.Season
		next.Episode = episodeNumbers[index]
		metas = append(metas, next)
	}
	return metas, nil
}
