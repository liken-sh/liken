package main

// series.go reads one folder per series into a series item, an episode item per
// episode, and a file per episode. A season is a grouping the media browser
// draws from the episodes' season numbers, so the walk records a season on each
// episode and mints no season item.

import (
	"fmt"
	"iter"
	"os"
	"path/filepath"
)

// walkSeries reads a whole series root into one walkResult by collecting the
// folder stream. The tests and a small library use this whole-root read.
func walkSeries(root, library string, ignore ignoreSet) *walkResult {
	return collectFolders(walkSeriesFolders(root, library, ignore))
}

// walkSeriesFolders streams a series root one series folder at a time. Every
// directory under the root is one series, with a tvshow.nfo, season folders, and
// an episode file with its own .nfo. A read error at the root yields one result
// marked with the error and nothing else, so the caller keeps the catalog.
func walkSeriesFolders(root, library string, ignore ignoreSet) iter.Seq[*walkResult] {
	return func(yield func(*walkResult) bool) {
		entries, err := os.ReadDir(root)
		if err != nil {
			yield(&walkResult{readError: true})
			return
		}
		for _, entry := range entries {
			if entry.IsDir() && !ignore.skips(entry.Name()) {
				folder := &walkResult{}
				scanSeriesFolder(root, filepath.Join(root, entry.Name()), library, ignore, folder)
				if !yield(folder) {
					return
				}
			}
		}
	}
}

// scanSeriesFolder reads one series folder into the result: the series item,
// and an episode item and a file for every episode under it. The identity comes
// from tvshow.nfo where the folder holds one, and from the folder name where it
// does not.
func scanSeriesFolder(root, dir, library string, ignore ignoreSet, result *walkResult) {
	name := filepath.Base(dir)
	meta, identified := seriesIdentity(dir, name)

	title := meta.Title
	if !identified {
		title = name
	}

	key := folderKey(name)
	seriesID := itemID(scopeSeries, meta.ProviderIDs, key)
	primaryArt, allArt := discoverArt(root, dir)

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

	for _, episode := range collectEpisodeFiles(dir, ignore) {
		scanEpisode(root, library, seriesID, episode, result)
	}
}

// seriesIdentity reads a series folder's identity, the same ladder the movies
// walk uses: a readable tvshow.nfo with a title, or the folder name, identified
// when the name yields a year.
func seriesIdentity(dir, name string) (seriesMeta, bool) {
	if data, err := os.ReadFile(filepath.Join(dir, "tvshow.nfo")); err == nil {
		if meta, err := parseSeriesNFO(data); err == nil && meta.Title != "" {
			return meta, true
		}
	}
	title, year := parseReleaseName(name)
	if title == "" {
		title = name
	}
	return seriesMeta{Title: title, Year: year, Released: releasedFromYear(year)}, year > 0
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
func collectEpisodeFiles(seriesDir string, ignore ignoreSet) []episodeFile {
	var files []episodeFile
	for _, file := range listVideoFiles(seriesDir) {
		files = append(files, episodeFile{dir: seriesDir, file: file})
	}
	entries, err := os.ReadDir(seriesDir)
	if err != nil {
		return files
	}
	for _, entry := range entries {
		if !entry.IsDir() || ignore.skips(entry.Name()) {
			continue
		}
		seasonDir := filepath.Join(seriesDir, entry.Name())
		for _, file := range listVideoFiles(seasonDir) {
			files = append(files, episodeFile{dir: seasonDir, file: file})
		}
	}
	return files
}

// scanEpisode reads one episode file into an episode item and a file. The season
// and episode numbers come from the episode .nfo where one sits beside the file,
// and from the season folder and the file name where none does. An episode the
// scanner cannot number is left out, because it has no place under the series.
func scanEpisode(root, library, seriesID string, episode episodeFile, result *walkResult) {
	meta := episodeIdentity(episode)
	if meta.Episode <= 0 {
		return
	}

	episodeItemID := episodeID(seriesID, meta.Season, meta.Episode)
	title := meta.Title
	if title == "" {
		title = fmt.Sprintf("S%02dE%02d", meta.Season, meta.Episode)
	}
	absolute := filepath.Join(episode.dir, episode.file)
	thumb := episodeThumb(root, episode.dir, episode.file)

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

	var stream *streamInfo
	if meta.Stream.present() {
		stream = &meta.Stream
	}
	container, videoCodec, audioCodec, width, height, durationMs := fileAttributes(episode.file, stream)
	result.files = append(result.files, fileRow{
		Path:       relativePath(root, absolute),
		Library:    library,
		Container:  container,
		VideoCodec: videoCodec,
		AudioCodec: audioCodec,
		Width:      width,
		Height:     height,
		SizeBytes:  fileSize(absolute),
		DurationMs: durationMs,
		Trickplay:  trickplayFor(root, episode.dir, episode.file),
		Present:    true,
		Items:      []string{episodeItemID},
	})
	result.aliases = append(result.aliases, aliasRowsForItem(scopeEpisode, meta.ProviderIDs, "", episodeItemID)...)
}

// episodeIdentity reads one episode's numbers and body. The .nfo beside the file
// is the source where one exists; the season folder and the file name fill a
// number the sidecar left at zero, so a sidecar that names only the episode
// still takes its season from the folder.
func episodeIdentity(episode episodeFile) episodeMeta {
	var meta episodeMeta
	nfoPath := filepath.Join(episode.dir, stripExtension(episode.file)+".nfo")
	if data, err := os.ReadFile(nfoPath); err == nil {
		if parsed, err := parseEpisodeNFO(data); err == nil {
			meta = parsed
		}
	}
	season, episodeNumber, marked := parseEpisodeMarker(episode.file)
	if meta.Season == 0 {
		if folderSeason, ok := parseSeasonFolder(filepath.Base(episode.dir)); ok {
			meta.Season = folderSeason
		} else if marked {
			meta.Season = season
		}
	}
	if meta.Episode == 0 && marked {
		meta.Episode = episodeNumber
	}
	return meta
}
