package main

// movies.go reads one folder per title into item, file, and alias rows. A
// movies volume holds title folders at its root or under grouping folders, as
// the lab's volume groups by genre, so the walk steps through a grouping
// folder until it reaches a title folder or the depth cap.
//
// It reads every file a title folder holds, and the extras folders beside the
// feature, and not the video files alone. files.go classifies each one.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// walkMovies reads a whole movies root into one walkResult by collecting the
// folder stream. The tests and a small library use this whole-root read.
func walkMovies(root, library string, ignore ignoreSet) *walkResult {
	return collectFolders(walkTree(context.Background(), root, movieFolderRule(root, library, ignore)))
}

// movieGroupingDepth bounds how deep the walk descends through grouping
// folders. A volume that groups by genre and then by studio nests a title
// two levels down, so the cap leaves room for that and more, and it stops a
// deep or looping tree from running the walk away.
const movieGroupingDepth = 8

// movieFolderRule is what the pool in walk.go needs to walk a movies volume. A
// directory that holds a movie.nfo or a video file is a title folder. A
// directory with neither is a grouping folder to descend into, down to the
// depth cap.
func movieFolderRule(root, library string, ignore ignoreSet) folderRule {
	return folderRule{
		isTitle: isMovieTitleFolder,
		scan: func(dir string, result *walkResult) {
			scanMovieFolder(root, dir, library, result)
		},
		ignore:   ignore,
		maxDepth: movieGroupingDepth,
	}
}

// isMovieTitleFolder reports whether a directory is a title folder: it holds a
// movie.nfo or a video file. A directory with neither is a grouping folder the
// walk steps through.
func isMovieTitleFolder(dir string) bool {
	if exists, err := fileExists(filepath.Join(dir, "movie.nfo")); err == nil && exists {
		return true
	}
	videos, err := listVideoFiles(dir)
	return err == nil && len(videos) > 0
}

// scanMovieFolder reads one title folder into the result: a movie row, a file
// row per video file, and the alias rows. The identity comes from movie.nfo
// where the folder holds one, and from the folder name where it does not. A
// folder that yields neither a sidecar nor a year is counted unidentified and
// cataloged by its folder name, so it is still browsable and the count is
// accurate.
func scanMovieFolder(root, dir, library string, result *walkResult) {
	name := filepath.Base(dir)
	meta, identified, err := movieIdentity(dir, name)
	// A folder whose sidecar could not be read has no identity this
	// pass, so it writes no row. A row written from the name alone would
	// carry a different id from the one the catalog holds. The walk is
	// already marked incomplete, so the rows the catalog holds stand.
	if err != nil {
		result.noteReadError(err)
		return
	}

	title := meta.Title
	if !identified {
		title = name
	}

	key := folderKey(name)
	id := itemID(scopeMovie, meta.ProviderIDs, key)
	relativeDir := relativePath(root, dir)
	primaryArt, allArt, err := discoverArt(root, dir)
	result.noteReadError(err)

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
		SetID:    meta.SetID,
		NFOFacts: meta.NFOFacts,
	})

	videos := map[string]bool{}
	files, err := listVideoFiles(dir)
	result.noteReadError(err)
	for i, video := range files {
		videos[video] = true
		stream, err := movieFileStream(dir, video, i, meta.Stream)
		result.noteReadError(err)
		if err != nil {
			continue
		}
		row, err := movieFileRow(root, dir, video, library, id, stream)
		result.noteReadError(err)
		if err != nil {
			continue
		}
		result.files = append(result.files, row)
	}

	scanMovieFiles(root, dir, library, id, videos, result)

	result.aliases = append(result.aliases, aliasRowsForItem(library, scopeMovie, meta.ProviderIDs, key, id)...)
	readLikenSidecar(likenSidecar{root: root, dir: dir, library: library, item: id}, result)
	result.titles++
	if !identified {
		result.unidentified++
		result.unidentifiedNames = append(result.unidentifiedNames, relativeDir)
	}
}

// movieIdentity reads a folder's identity. A readable movie.nfo with a title
// is the identity, and the folder is identified. A folder with no usable
// sidecar falls back to the name parse, and it is identified only when the
// name yields a year or a provider id, the signal that the parse read a real
// release and not an arbitrary folder. A name's provider ids fill what the
// sidecar left out, so a person confirms a candidate by naming the folder in
// Jellyfin's form. A sidecar that is not there falls through to the name
// parse. A sidecar the scanner cannot read is an error, because falling
// through would mint a different id and sweep the title's own rows.
func movieIdentity(dir, name string) (movieMeta, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, "movie.nfo"))
	switch {
	case err == nil:
		if meta, err := parseMovieNFO(data); err == nil && meta.Title != "" {
			meta.ProviderIDs = mergeProviderIDs(meta.ProviderIDs, parseProviderIDs(name))
			return meta, true, nil
		}
	case !errors.Is(err, fs.ErrNotExist):
		return movieMeta{}, false, err
	}
	title, year := parseReleaseName(name)
	if title == "" {
		title = name
	}
	ids := parseProviderIDs(name)
	return movieMeta{
		Title: title, Year: year, Released: releasedFromYear(year), ProviderIDs: ids,
	}, year > 0 || len(ids) > 0, nil
}

// releasedFromYear renders a year as the released column, or leaves it empty
// where there is no year.
func releasedFromYear(year int) string {
	if year <= 0 {
		return ""
	}
	return strconv.Itoa(year)
}

// movie.nfo describes the first video of a title folder, and the sidecar
// beside a file describes every other video. That is the same division the
// probe writes them under.
func movieFileStream(dir, file string, index int, folder streamInfo) (*streamInfo, error) {
	if index == 0 {
		if folder.present() {
			return &folder, nil
		}
		return nil, nil
	}
	return streamBeside(dir, file)
}

// movieFileRow reads one video file into a file row linked to its movie. The
// technical attributes come from the sidecar's streamdetails where one was
// present, and from the file name where none was.
func movieFileRow(root, dir, file, library, itemID string, stream *streamInfo) (fileRow, error) {
	container, videoCodec, audioCodec, width, height, durationMs := fileAttributes(file, stream)
	absolute := filepath.Join(dir, file)
	size, modified, err := statFile(absolute)
	if err != nil {
		return fileRow{}, err
	}
	class := classifyFile(file, filePlace{kind: libraryKindMovies})
	return fileRow{
		Path:       relativePath(root, absolute),
		Library:    library,
		Container:  container,
		VideoCodec: videoCodec,
		AudioCodec: audioCodec,
		Width:      width,
		Height:     height,
		SizeBytes:  size,
		DurationMs: durationMs,
		Trickplay:  trickplayFor(root, dir, file),
		Present:    true,
		Type:       class.Type,
		Role:       class.Role,
		Modified:   modified,
		Items:      []string{itemID},
	}, nil
}

// scanMovieFiles reads the rest of a movie title folder: the sidecar, the art,
// the subtitles, the trickplay directory, and the extras folders beside the
// feature. Every one of them links to the movie.
func scanMovieFiles(root, dir, library, itemID string, videos map[string]bool, result *walkResult) {
	rows, subdirectories, err := folderFiles{
		root:    root,
		dir:     dir,
		library: library,
		place:   filePlace{kind: libraryKindMovies},
		item:    constantItem(itemID),
		held:    videos,
	}.read()
	result.noteReadError(err)
	result.files = append(result.files, rows...)

	for _, name := range subdirectories {
		extras := extrasFolderName(name)
		if extras == "" {
			continue
		}
		rows, _, err := folderFiles{
			root:    root,
			dir:     filepath.Join(dir, name),
			library: library,
			place:   filePlace{kind: libraryKindMovies, extras: extras},
			item:    constantItem(itemID),
		}.read()
		result.noteReadError(err)
		result.files = append(result.files, rows...)
	}
}
