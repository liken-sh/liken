package main

// How the credits fact finds a credited person's entry by id: the catalog's
// contributor_ids table first, then TMDb's find call for a credit that holds
// only an IMDb id. A credit from IMDb's datasets names a person by the IMDb id
// alone, and a lookup by id keeps that credit off a second entry for a person
// the store already holds.

import (
	"context"
	"path/filepath"
	"slices"
)

// How many mergedInto fields a lookup follows. A merge names an entry that
// stays, and a later merge can remove that entry too, so a record can name a
// record. The bound stops a loop that a hand edit made.
const mergeHops = 8

// The entry the catalog resolves one of the ids to, or an empty string. The
// schemes are read in the order the slug suffix prefers them, then the rest
// in name order, so the answer is the same on every run. A catalog that
// cannot be read answers nothing, and the slug join runs as it would with no
// catalog.
func (e *enricher) contributorByID(ids providerIDs) string {
	if e.catalog == nil {
		return ""
	}
	for _, scheme := range idSchemes(ids) {
		paths, err := e.catalog.contributorsWithID(context.Background(), e.library, scheme, ids[scheme])
		if err != nil {
			e.logf("could not read the entries of %s %s: %v", scheme, ids[scheme], err)
			return ""
		}
		for _, path := range paths {
			if stays := e.entryThatStays(path); stays != "" {
				return stays
			}
		}
	}
	return ""
}

// The schemes of one set of ids that hold an id: TMDb, then IMDb, then the
// rest in name order.
func idSchemes(ids providerIDs) []string {
	var schemes []string
	for _, scheme := range contributorSchemes {
		if ids[scheme] != "" {
			schemes = append(schemes, scheme)
		}
	}
	for _, scheme := range sortedKeys(ids) {
		if ids[scheme] != "" && !slices.Contains(contributorSchemes, scheme) {
			schemes = append(schemes, scheme)
		}
	}
	return schemes
}

// The entry a path names on the volume: the path itself when its
// contributor.yaml is a person, or the entry its mergedInto names. A path whose
// file is not on the volume, or a chain of records longer than the bound,
// answers an empty string, because the catalog row that named it is older
// than the volume.
func (e *enricher) entryThatStays(path string) string {
	for range mergeHops {
		held, data, err := readContributorFile(filepath.Join(e.root, path, contributorFileName))
		if err != nil || data == nil {
			return ""
		}
		if held.MergedInto == "" {
			return path
		}
		path = held.MergedInto
	}
	return ""
}

// The TMDb id of a credit that holds an IMDb id and no TMDb id, from TMDb's
// find call, where the Library's sources name a Ready TMDb provider. A call
// that fails answers nothing, and the credit keeps the ids it came with.
func (e *enricher) findCreditedTMDb(ids providerIDs) string {
	imdb := ids[contributorIMDbScheme]
	if e.personFinder == nil || imdb == "" || ids[contributorTMDbScheme] != "" {
		return ""
	}
	found, err := e.personFinder.personByIMDb(context.Background(), imdb)
	if err != nil {
		e.logf("could not find the TMDb id of %s: %v", imdb, err)
		return ""
	}
	return found
}

// The TMDb client the credits fact asks for a person's TMDb id: the TMDb
// account the Library's sources reach, or none. LIBRARY_SOURCES names only the
// blocks of Ready providers, and the key arrives as TMDB_TOKEN.
func newPersonFinder(sources []string, value func(string) string, record *tallies) *tmdbClient {
	if !slices.Contains(sources, providerBlockTMDb) {
		return nil
	}
	token := value(providerTokenVariable(providerBlockTMDb))
	if token == "" {
		return nil
	}
	client := newTMDbClient(tmdbAPIBase, token)
	client.recordTo(record)
	return client
}

// A copy of the ids with one more id, because the ids of a credit are the
// provider's answer, which other credits of the title share.
func withID(ids providerIDs, scheme, id string) providerIDs {
	copied := make(providerIDs, len(ids)+1)
	for key, value := range ids {
		copied[key] = value
	}
	copied[scheme] = id
	return copied
}
