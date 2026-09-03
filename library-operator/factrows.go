package main

// A fact writes the rows for what it wrote. After a fact writes a folder, it
// reads that folder into rows through the reader the scan uses and writes
// only the columns it owns, so the catalog holds the fact minutes after the
// file does and no reader waits for the next walk. The files stay the truth.
// These rows are the projection the scan makes of them, made sooner, and the
// next walk writes the same values again.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The reader's view of this library: what the scan's per-folder readers take
// beside the folder.
func (e *enricher) folderScan() folderScan {
	return folderScan{root: e.root, library: e.library, kind: e.kind, ignore: e.ignore}
}

// The rows of one folder, through the reader the walk uses. A contributor
// fact reads a person's directory; every other fact reads the title folder
// that holds the path it wrote. A container with no catalog, which a test
// builds, reads nothing.
func (e *enricher) rowsOf(fact, folder string) *walkResult {
	if _, person := contributorFactSet[fact]; person {
		result := &walkResult{}
		readContributorFolder(e.root, e.library, folder, result)
		return result
	}
	return readFolder(e.folderScan(), e.titleFolder(folder))
}

var contributorFactSet = map[string]bool{
	factContributorIDs: true, factContributorBiography: true, factContributorHeadshot: true,
}

// The rows one fact owns, written after its ledger. Every fact writes its own
// attempt row. A fact that wrote a file also writes the columns it owns, from
// the folder as it stands now. Where the catalog refuses a write, the run
// goes on, because the files hold the truth and the next walk writes the same
// rows.
func (e *enricher) writeRows(fact, folder string, wrote bool) {
	if e.catalog == nil {
		return
	}
	result := e.rowsOf(fact, folder)
	if result == nil {
		return
	}
	ctx := context.Background()
	if wrote {
		if err := e.writeOwnedRows(ctx, fact, result); err != nil {
			e.logf("could not write the %s rows of %s: %v", fact, relativePath(e.root, folder), err)
		}
	}
	var attempts []attemptRow
	for _, attempt := range result.attempts {
		if attempt.Fact == fact {
			attempts = append(attempts, attempt)
		}
	}
	if _, err := e.catalog.UpsertAttempts(ctx, attempts); err != nil {
		e.logf("could not write the %s attempt row of %s: %v", fact, relativePath(e.root, folder), err)
	}
}

// Which columns each fact owns, plan 34's table, as the statements that write
// them.
func (e *enricher) writeOwnedRows(ctx context.Context, fact string, result *walkResult) error {
	_, art := artTypes[fact]
	switch {
	case fact == factProbe:
		return e.writeProbeRows(ctx, result)
	case fact == factTrickplay:
		_, err := e.catalog.UpdateFileTrickplay(ctx, filesOfType(result.files, fileTypeVideo))
		return err
	case fact == factCredits:
		if err := e.writeBodyRows(ctx, result); err != nil {
			return err
		}
		return e.writeCreditRows(ctx, result)
	case nfoFactSet[fact]:
		return e.writeBodyRows(ctx, result)
	case art:
		return e.writeArtRows(ctx, result)
	case contributorFactSet[fact]:
		if _, err := e.catalog.UpdateContributorFacts(ctx, result.contributors); err != nil {
			return err
		}
		_, err := e.catalog.UpsertContributorAliases(ctx, result.contributorAliases)
		return err
	}
	return nil
}

var nfoFactSet = map[string]bool{
	factOverview: true, factCertification: true, factRatingTMDb: true, factRatingIMDb: true,
	factRatingRottenTomatoes: true, factRatingMetacritic: true,
}

func filesOfType(files []fileRow, kind string) []fileRow {
	var held []fileRow
	for _, file := range files {
		if file.Type == kind {
			held = append(held, file)
		}
	}
	return held
}

// The probe owns the stream columns of every video and the duration of the
// items whose sidecar states no runtime of its own.
func (e *enricher) writeProbeRows(ctx context.Context, result *walkResult) error {
	if _, err := e.catalog.UpdateFileStreams(ctx, filesOfType(result.files, fileTypeVideo)); err != nil {
		return err
	}
	var movies, episodes []itemUpdate
	for _, row := range result.movies {
		movies = append(movies, itemUpdate{Library: row.Library, Id: row.Id, Values: []any{row.Duration}})
	}
	for _, row := range result.episodes {
		episodes = append(episodes, itemUpdate{Library: row.Library, Id: row.Id, Values: []any{row.Duration}})
	}
	if _, err := e.catalog.UpdateItemDurations(ctx, "movies", movies); err != nil {
		return err
	}
	_, err := e.catalog.UpdateItemDurations(ctx, "episodes", episodes)
	return err
}

// The nfo phase owns the body and the nfo_facts of the title itself. The
// phase edits no episode sidecar, so the episode rows stay as they are.
func (e *enricher) writeBodyRows(ctx context.Context, result *walkResult) error {
	var rows []itemUpdate
	for _, row := range result.movies {
		rows = append(rows, bodyUpdate(row.Library, row.Id, row.Body, row.NFOFacts))
	}
	for _, row := range result.series {
		rows = append(rows, bodyUpdate(row.Library, row.Id, row.Body, row.NFOFacts))
	}
	_, err := e.catalog.UpdateItemBodies(ctx, itemTable(e.kind), rows)
	return err
}

// The credits fact owns the credits rows of the title, as one set per item.
func (e *enricher) writeCreditRows(ctx context.Context, result *walkResult) error {
	byItem := map[string][]creditRow{}
	for _, row := range result.credits {
		byItem[row.Item] = append(byItem[row.Item], row)
	}
	for _, row := range result.movies {
		if _, err := e.catalog.ReplaceCredits(ctx, row.Library, row.Id, byItem[row.Id]); err != nil {
			return err
		}
	}
	for _, row := range result.series {
		if _, err := e.catalog.ReplaceCredits(ctx, row.Library, row.Id, byItem[row.Id]); err != nil {
			return err
		}
	}
	return nil
}

// The art phase owns the image files' own rows and links, and the art columns
// of every item the folder holds: a title's poster changes the title's art,
// and a season poster or an episode thumbnail changes an episode's.
func (e *enricher) writeArtRows(ctx context.Context, result *walkResult) error {
	images := filesOfType(result.files, fileTypeImage)
	if _, err := e.catalog.UpsertFiles(ctx, images); err != nil {
		return err
	}
	if _, err := e.catalog.UpsertFileItems(ctx, images); err != nil {
		return err
	}
	var titles, episodes []itemUpdate
	for _, row := range result.movies {
		titles = append(titles, artUpdate(row.Library, row.Id, row.Art, row.Arts))
	}
	for _, row := range result.series {
		titles = append(titles, artUpdate(row.Library, row.Id, row.Art, row.Arts))
	}
	for _, row := range result.episodes {
		episodes = append(episodes, artUpdate(row.Library, row.Id, row.Art, row.Arts))
	}
	if _, err := e.catalog.UpdateItemArt(ctx, itemTable(e.kind), titles); err != nil {
		return err
	}
	_, err := e.catalog.UpdateItemArt(ctx, "episodes", episodes)
	return err
}

// The person's row and ids, written the moment the credits fact creates the
// entry, so the contributors phase in the same run finds the person in its
// gap. The row lands only where none exists. The contributors phase owns its
// columns from then on.
func (e *enricher) writePersonRows(directory string) {
	if e.catalog == nil {
		return
	}
	result := &walkResult{}
	readContributorFolder(e.root, e.library, filepath.Join(e.root, directory), result)
	ctx := context.Background()
	if _, err := e.catalog.InsertContributors(ctx, result.contributors); err != nil {
		e.logf("could not write the row of %s: %v", directory, err)
		return
	}
	if _, err := e.catalog.UpsertContributorAliases(ctx, result.contributorAliases); err != nil {
		e.logf("could not write the ids of %s: %v", directory, err)
	}
}

// The identity fact rescans its folder rather than updating a column, because
// the id it wrote is the key of every other row of the title. The scan's own
// single-folder path reads the rows, writes them, and prunes the rows under
// the old key.
func (e *enricher) rescanTitle(folder string) {
	if e.catalog == nil {
		return
	}
	title := e.titleFolder(folder)
	if _, _, err := rescanFolder(context.Background(), e.catalog, e.folderScan(), title); err != nil {
		e.logf("could not rescan %s after its identity: %v", relativePath(e.root, folder), err)
	}
}

// The title or series folder that holds a path on the volume, which is the
// folder the walk reads as one unit, and whether one was found. A series
// folder is a child of the root. A movie folder is the first level down that
// is a title folder, no deeper than the walk's grouping cap. A level that
// left the volume is that folder, because a rescan of it takes its rows.
func titleFolderOf(root, kind, absolute string) (string, bool) {
	relative := relativePath(root, absolute)
	if relative == absolute || relative == "." || strings.HasPrefix(relative, "..") {
		return "", false
	}
	parts := splitPath(relative)
	if len(parts) == 0 {
		return "", false
	}
	if kind == libraryKindSeries {
		return path.Join(root, parts[0]), true
	}
	folder := root
	for depth, part := range parts {
		if depth > movieGroupingDepth {
			return "", false
		}
		folder = path.Join(folder, part)
		info, err := os.Stat(folder)
		if errors.Is(err, fs.ErrNotExist) {
			return folder, true
		}
		if err != nil || !info.IsDir() {
			return "", false
		}
		if isMovieTitleFolder(folder) {
			return folder, true
		}
	}
	return "", false
}

// The folder a fact reads after its write: the title folder that holds the
// path, or the path itself where no title folder holds it, so a fact always
// reads what it was given.
func (e *enricher) titleFolder(absolute string) string {
	if folder, held := titleFolderOf(e.root, e.kind, absolute); held {
		return folder
	}
	return absolute
}
