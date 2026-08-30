package main

// identity.go rolls every name a title carries onto its one canonical id, so a
// lookup by any of its provider ids or its folder key resolves the same work.
// aliasesFor in rows.go covers the providers in the canonical order; this file
// adds the rest, so a movie that also lists a tvdb id still resolves by it.

import "sort"

// aliasRowsForItem builds every alias an item carries. It starts with
// aliasesFor, which reads the providers in the canonical order and the folder
// key, then adds an alias for every other provider id the sidecar named. The
// extra providers are added in sorted order, so a re-walk of the same sidecar
// writes the same rows.
func aliasRowsForItem(kind string, providerIDs map[string]string, folderKey, canonicalID string) []aliasRow {
	rows := aliasesFor(kind, providerIDs, folderKey, canonicalID)
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
		rows = append(rows, aliasRow{Alias: alias, Item: canonicalID, Source: aliasSourceProvider})
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
}
