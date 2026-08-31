package main

// identity.go rolls every name a title carries onto its one canonical id, so a
// lookup by any of its provider ids or its folder key resolves the same work.
// aliasesFor in rows.go covers the providers in the canonical order; this file
// adds the rest, so a movie that also lists a tvdb id still resolves by it.

import (
	"iter"
	"sort"
)

// aliasRowsForItem builds every alias an item carries. It starts with
// aliasesFor, which reads the providers in the canonical order and the folder
// key, then adds an alias for every other provider id the sidecar named. The
// extra providers are added in sorted order, so a re-walk of the same sidecar
// writes the same rows.
func aliasRowsForItem(library, kind string, providerIDs map[string]string, folderKey, canonicalID string) []aliasRow {
	rows := aliasesFor(library, kind, providerIDs, folderKey, canonicalID)
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.Alias] = true
	}
	extras := make([]string, 0, len(providerIDs))
	for provider, value := range providerIDs {
		if value == "" {
			continue
		}
		alias := kind + ":" + provider + ":" + value
		if !seen[alias] {
			extras = append(extras, alias)
		}
	}
	sort.Strings(extras)
	for _, alias := range extras {
		rows = append(rows, aliasRow{Alias: alias, Library: library, Item: canonicalID, Source: aliasSourceProvider})
	}
	return rows
}

// walkResult is what one walk read off the volume: the item rows per kind, the
// file rows and their item links, the alias rows, and the two counts the report
// carries.
type walkResult struct {
	movies       []movieRow
	series       []seriesRow
	episodes     []episodeRow
	files        []fileRow
	aliases      []aliasRow
	titles       int
	unidentified int
	// the paths of the folders this walk could not identify, so a
	// full walk names a sample of them in its log without holding every
	// one. It carries one path per unidentified folder.
	unidentifiedNames []string
	// A walk that could not read a directory, a sidecar, or a file read
	// only part of the volume, whatever the depth of the failure. The
	// prune-abort guard then skips the prune for this pass and keeps the
	// rows the walk did not reach.
	readError bool
}

// noteReadError folds one failed read into the walk's incomplete mark. A
// folder the scanner could not read in full must never sweep as departed,
// and the mark is what holds the prune back.
func (r *walkResult) noteReadError(err error) {
	if err != nil {
		r.readError = true
	}
}

// appendFolder folds one folder's rows into a running buffer, so the streaming
// full walk gathers several folders before it writes them in one batch and never
// holds the whole library.
func appendFolder(buffer, folder *walkResult) {
	buffer.movies = append(buffer.movies, folder.movies...)
	buffer.series = append(buffer.series, folder.series...)
	buffer.episodes = append(buffer.episodes, folder.episodes...)
	buffer.files = append(buffer.files, folder.files...)
	buffer.aliases = append(buffer.aliases, folder.aliases...)
}

// collectFolders reads a whole folder stream into one walkResult, with the
// counts and the read-error signal. The tests and a small library read a root
// this way.
func collectFolders(folders iter.Seq[*walkResult]) *walkResult {
	result := &walkResult{}
	for folder := range folders {
		appendFolder(result, folder)
		result.titles += folder.titles
		result.unidentified += folder.unidentified
		if folder.readError {
			result.readError = true
		}
	}
	return result
}
