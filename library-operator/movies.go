package main

// movies.go reads one folder per title into item, file, and alias rows. A
// movies volume holds title folders at its root or under one level of grouping
// folders, as the lab's volume groups by genre, so the walk descends one level
// and no deeper.

import (
	"os"
	"path/filepath"
	"strconv"
)

// walkMovies reads a movies root into a walkResult. A directory under the root
// that holds a movie.nfo or a video file is a title folder. A directory that
// holds neither is a grouping folder, and its own children are title folders.
// The walk descends one level, because the lab groups by genre and no volume
// nests deeper.
func walkMovies(root, library string, ignore ignoreSet) *walkResult {
	result := &walkResult{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return result
	}
	for _, entry := range entries {
		if !entry.IsDir() || ignore.skips(entry.Name()) {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if isMovieTitleFolder(dir) {
			scanMovieFolder(root, dir, library, result)
			continue
		}
		grouped, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, child := range grouped {
			if child.IsDir() && !ignore.skips(child.Name()) {
				scanMovieFolder(root, filepath.Join(dir, child.Name()), library, result)
			}
		}
	}
	return result
}

// isMovieTitleFolder reports whether a directory is a title folder: it holds a
// movie.nfo or a video file. A directory with neither is a grouping folder the
// walk steps through.
func isMovieTitleFolder(dir string) bool {
	if fileExists(filepath.Join(dir, "movie.nfo")) {
		return true
	}
	return len(listVideoFiles(dir)) > 0
}

// scanMovieFolder reads one title folder into the result: a movie row, a file
// row per video file, and the alias rows. The identity comes from movie.nfo
// where the folder holds one, and from the folder name where it does not. A
// folder that yields neither a sidecar nor a year is counted unidentified and
// cataloged by its folder name, so it is still browsable and the count is
// accurate.
func scanMovieFolder(root, dir, library string, result *walkResult) {
	name := filepath.Base(dir)
	meta, identified := movieIdentity(dir, name)

	title := meta.Title
	if !identified {
		title = name
	}

	key := folderKey(name)
	id := itemID(scopeMovie, meta.ProviderIDs, key)
	relativeDir := relativePath(root, dir)
	primaryArt, allArt := discoverArt(root, dir)

	body := meta.Body
	body.ProviderIDs = meta.ProviderIDs
	body.Art = allArt

	result.movies = append(result.movies, movieRow{
		Id:       id,
		Library:  library,
		Kind:     libraryKindMovies,
		Path:     relativeDir,
		Title:    title,
		SortKey:  sortKey(title),
		Slug:     slug(title, meta.Year),
		Released: meta.Released,
		Added:    addedTime(dir),
		Art:      primaryArt,
		Duration: meta.Duration,
		Body:     body,
	})

	// The sidecar's streamdetails describe the primary file, so only the first
	// video file reads its attributes from the sidecar; the rest read them from
	// their own name.
	for i, video := range listVideoFiles(dir) {
		var stream *streamInfo
		if i == 0 && meta.Stream.present() {
			stream = &meta.Stream
		}
		result.files = append(result.files, movieFileRow(root, dir, video, library, id, stream))
	}

	result.aliases = append(result.aliases, aliasRowsForItem(scopeMovie, meta.ProviderIDs, key, id)...)
	result.titles++
	if !identified {
		result.unidentified++
	}
}

// movieIdentity reads a folder's identity. A readable movie.nfo with a title is
// the identity, and the folder is identified. A folder with no usable sidecar
// falls back to the name parse, and it is identified only when the name yields
// a year, the signal that the parse read a real release and not an arbitrary
// folder.
func movieIdentity(dir, name string) (movieMeta, bool) {
	if data, err := os.ReadFile(filepath.Join(dir, "movie.nfo")); err == nil {
		if meta, err := parseMovieNFO(data); err == nil && meta.Title != "" {
			return meta, true
		}
	}
	title, year := parseReleaseName(name)
	if title == "" {
		title = name
	}
	return movieMeta{Title: title, Year: year, Released: releasedFromYear(year)}, year > 0
}

// releasedFromYear renders a year as the released column, or leaves it empty
// where there is no year.
func releasedFromYear(year int) string {
	if year <= 0 {
		return ""
	}
	return strconv.Itoa(year)
}

// movieFileRow reads one video file into a file row linked to its movie. The
// technical attributes come from the sidecar's streamdetails where one was
// present, and from the file name where none was.
func movieFileRow(root, dir, file, library, itemID string, stream *streamInfo) fileRow {
	container, videoCodec, audioCodec, width, height, durationMs := fileAttributes(file, stream)
	absolute := filepath.Join(dir, file)
	return fileRow{
		Path:       relativePath(root, absolute),
		Library:    library,
		Container:  container,
		VideoCodec: videoCodec,
		AudioCodec: audioCodec,
		Width:      width,
		Height:     height,
		SizeBytes:  fileSize(absolute),
		DurationMs: durationMs,
		Trickplay:  trickplayFor(root, dir, file),
		Present:    true,
		Items:      []string{itemID},
	}
}
