package main

// identityids.go fills the ids a title lacks once it holds one. A title that
// came from Radarr, Sonarr, or a person often names one database alone, and
// every provider that keys on another database's id passes it over. TMDb's
// find call turns an IMDb or a TheTVDB id into a TMDb id, and TMDb's external
// ids call gives the rest.

import (
	"context"
	"encoding/xml"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The identity gap is every title that lacks one of the ids its scope's
// canonical order names, which are the databases liken keys a title on. A
// title with a path id holds none of them, so the ladder's titles are in it
// too. No subquery reads the outer row, so SQLite builds each one once.
func identityGapSQL() string {
	var missing []string
	for _, scope := range []string{scopeMovie, scopeSeries} {
		for _, provider := range providerOrder[scope] {
			missing = append(missing, `(id LIKE '`+scope+`:%' AND id NOT IN `+
				`(SELECT item FROM aliases WHERE aliases.library = ?1 AND alias LIKE '`+scope+`:`+provider+`:%'))`)
		}
	}
	// The condition stays in the outer query, not the source, so a refresh
	// opens a title that holds every id.
	return `SELECT id FROM (` +
		`SELECT library, id FROM movies ` +
		`UNION ALL SELECT library, id FROM series) AS items ` +
		`WHERE library = ?1 AND ` + gapClause(factIdentity, "id", strings.Join(missing, " OR "))
}

// The ids TMDb's find call accepts, in the order the fact asks them: the
// database each kind's canonical order trusts first after TMDb.
var findSources = map[string][]string{
	libraryKindMovies: {"imdb", "tvdb"},
	libraryKindSeries: {"tvdb", "imdb"},
}

// A title that holds a TMDb id or an id the find call accepts takes the
// find path, and any other title climbs the ladder by its name.
func (e *enricher) holdsAnID(held providerIDs) bool {
	return held["tmdb"] != "" || slices.ContainsFunc(findSources[e.kind], func(provider string) bool {
		return held[provider] != ""
	})
}

// The provider ids a title's aliases carry, which are the ids its .nfo file
// named when the scanner last read it.
func (c *Catalog) titleIDs(ctx context.Context, library, id string) (providerIDs, error) {
	aliases, err := c.queryStrings(ctx, `SELECT alias FROM aliases WHERE library = ? AND item = ?`,
		[]any{library, id})
	if err != nil {
		return nil, err
	}
	return aliasIDs(aliases), nil
}

// What TMDb's find call answers for one id from another database. The call
// answers every kind of record that holds the id, and the kind the library
// holds is the one read.
type tmdbTitleFind struct {
	MovieResults []tmdbResult `json:"movie_results"`
	TVResults    []tmdbResult `json:"tv_results"`
}

// The title one IMDb or TheTVDB id names, with an id of zero where TMDb names
// no title of this kind for it.
func (c *tmdbClient) titleByID(ctx context.Context, kind, provider, id string) (tmdbResult, error) {
	var answer tmdbTitleFind
	query := url.Values{"external_source": {provider + "_id"}}
	if err := c.get(ctx, "/3/find/"+url.PathEscape(id), query, &answer); err != nil {
		return tmdbResult{}, err
	}
	results := answer.MovieResults
	if kind == libraryKindSeries {
		results = answer.TVResults
	}
	for _, result := range results {
		if result.ID > 0 {
			return result, nil
		}
	}
	return tmdbResult{}, nil
}

// One TMDb title the find call named, with each call that named it, as
// "tvdb 113981".
type foundTitle struct {
	result tmdbResult
	calls  []string
}

// A title that holds an id keeps it. The fact asks TMDb only for the ids it
// lacks, and never searches by name, because a name search could contradict
// the id a person or a tool wrote.
func (e *enricher) fillIDs(ctx context.Context, client *tmdbClient, item identityItem, held providerIDs) {
	folder := filepath.Join(e.root, item.path)
	if held["tmdb"] != "" {
		e.writeOtherIDs(ctx, client, folder, item, held, "its tmdb id", false)
		return
	}
	if e.restoreIDs(folder, item, held) {
		return
	}
	found, err := e.findTMDbIDs(ctx, client, held)
	if err != nil {
		e.logf("could not find %s by its ids: %v", opaqueID(item.id), err)
		e.recordIdentity(folder, nil, attemptError)
		return
	}
	switch len(found) {
	case 0:
		e.logf("tmdb names no title for the ids of %s", opaqueID(item.id))
		e.recordIdentity(folder, nil, attemptNothing)
	case 1:
		e.writeFoundID(ctx, client, folder, item, held, found[0])
	default:
		e.logf("%s waits for a person, because its ids name %d titles on tmdb", opaqueID(item.id), len(found))
		e.recordIdentity(folder, &likenItem{Path: likenSelfPath, Candidates: foundCandidates(found)}, attemptCandidates)
	}
}

// Every id the title holds is asked, so two ids that name two different
// titles on TMDb leave the choice to a person instead of trusting the
// first.
func (e *enricher) findTMDbIDs(ctx context.Context, client *tmdbClient, held providerIDs) ([]foundTitle, error) {
	byID := map[int]*foundTitle{}
	var order []int
	for _, provider := range findSources[e.kind] {
		if held[provider] == "" {
			continue
		}
		result, err := client.titleByID(ctx, e.kind, provider, held[provider])
		if err != nil {
			return nil, err
		}
		if result.ID == 0 {
			continue
		}
		call := provider + " " + held[provider]
		if title, seen := byID[result.ID]; seen {
			title.calls = append(title.calls, call)
			continue
		}
		byID[result.ID] = &foundTitle{result: result, calls: []string{call}}
		order = append(order, result.ID)
	}
	found := make([]foundTitle, 0, len(order))
	for _, id := range order {
		found = append(found, *byID[id])
	}
	return found, nil
}

func (e *enricher) writeFoundID(ctx context.Context, client *tmdbClient, folder string,
	item identityItem, held providerIDs, found foundTitle) {
	id := strconv.Itoa(found.result.ID)
	nfoPath, rootElement := identityNFO(e.kind, folder)
	if err := e.writeUniqueID(nfoPath, rootElement, item.title, "tmdb", id); err != nil {
		e.logf("could not write the id of %s: %v", opaqueID(item.id), err)
		e.recordIdentity(folder, nil, attemptError)
		return
	}
	reason := "find by " + strings.Join(found.calls, " and ")
	e.logf("identified %s as tmdb %s, by %s", opaqueID(item.id), id, reason)
	held["tmdb"] = id
	e.writeOtherIDs(ctx, client, folder, item, held, reason, true)
}

// The receipt on each candidate names the calls that named it, so a person
// reads which of the title's ids points where.
func foundCandidates(found []foundTitle) []likenCandidate {
	candidates := make([]likenCandidate, 0, len(found))
	for _, title := range found {
		candidates = append(candidates, likenCandidate{
			ID:      providerIDs{"tmdb": strconv.Itoa(title.result.ID)},
			Title:   title.result.name(),
			Year:    title.result.year(),
			Receipt: map[string]string{"find": strings.Join(title.calls, " and ")},
		})
	}
	return candidates
}

// The other databases' ids follow the TMDb id: one call to TMDb's external
// ids gives them, each one the title lacks goes into the .nfo file as its own
// uniqueid, and the scanner lifts every one into aliases. An id the title
// already holds stays, whatever TMDb answers for it.
//
// A failed call leaves an error, or a partial attempt where this attempt
// wrote the TMDb id. Both take the error window, so the next day's run asks
// again for the ids this one did not get.
func (e *enricher) writeOtherIDs(ctx context.Context, client *tmdbClient, folder string,
	item identityItem, ids providerIDs, reason string, wrote bool) {
	tmdbID, _ := strconv.Atoi(ids["tmdb"])
	external, err := client.externalIDs(ctx, e.kind, tmdbID)
	result := attemptFound
	if err != nil {
		e.logf("could not read the other ids of %s: %v", opaqueID(item.id), err)
		if !wrote {
			e.recordIdentity(folder, nil, attemptError)
			return
		}
		result = attemptPartial
	}
	e.writeLackingIDs(folder, item, ids, external.providerIDs())
	e.recordIdentity(folder, &likenItem{
		Path: likenSelfPath, ID: ids, Reason: reason, Written: time.Now().UTC(),
	}, result)
}

// Each id in others that the title lacks goes into its .nfo file, and into
// ids, which the ledger records. An id that will not write is logged and left
// for the next run.
func (e *enricher) writeLackingIDs(folder string, item identityItem, ids, others providerIDs) {
	nfoPath, rootElement := identityNFO(e.kind, folder)
	for _, provider := range sortedKeys(others) {
		if ids[provider] != "" || others[provider] == "" {
			continue
		}
		if err := e.writeUniqueID(nfoPath, rootElement, item.title, provider, others[provider]); err != nil {
			e.logf("could not write the %s id of %s: %v", provider, opaqueID(item.id), err)
			continue
		}
		ids[provider] = others[provider]
	}
}

// Sonarr rewrites tvshow.nfo when it refreshes a series, and the rewrite
// drops every uniqueid Sonarr's own record lacks, the TMDb id among them. The
// identity ledger beside the folder still holds the ids this fact found, so
// the fact writes them back without asking TMDb. The ledger stands only
// while every id it shares with the .nfo file agrees: an id a person changed
// means a different title, and the find call asks again.
func (e *enricher) restoreIDs(folder string, item identityItem, held providerIDs) bool {
	ledger, err := readLikenLedger(folder, factIdentity)
	if err != nil {
		return false
	}
	recorded, ok := ledger.itemAt(likenSelfPath)
	if !ok || recorded.ID["tmdb"] == "" {
		return false
	}
	for provider, id := range held {
		if other, shared := recorded.ID[provider]; shared && other != id {
			return false
		}
	}
	ids := maps.Clone(held)
	e.writeLackingIDs(folder, item, ids, recorded.ID)
	if ids["tmdb"] == "" {
		e.recordIdentity(folder, nil, attemptError)
		return true
	}
	e.logf("wrote back the ids of %s from its identity ledger", opaqueID(item.id))
	e.recordIdentity(folder, &likenItem{
		Path: likenSelfPath, ID: ids, Reason: recorded.Reason, Written: time.Now().UTC(),
	}, attemptFound)
	return true
}

// The default mark tells a reader which id to take first. The TMDb id takes
// it only where no uniqueid in the .nfo file carries it, so a file never
// holds two, and a file Sonarr wrote keeps its TheTVDB id first.
func (e *enricher) writeUniqueID(nfoPath, rootElement, title, provider, id string) error {
	element := fmt.Appendf(nil, `<uniqueid type=%q>%s</uniqueid>`, provider, id)
	if provider == "tmdb" && !nfoHoldsDefault(nfoPath) {
		element = fmt.Appendf(nil, `<uniqueid type=%q default="true">%s</uniqueid>`, provider, id)
	}
	return e.writer.editNFO(nfoPath, rootElement, title,
		xmlElement{name: "uniqueid", attribute: "type", value: provider}, element)
}

// A file that is not there or does not parse holds no mark, and the edit
// that follows reports the parse error.
func nfoHoldsDefault(nfoPath string) bool {
	data, err := os.ReadFile(nfoPath)
	if err != nil {
		return false
	}
	var ids struct {
		UniqueIDs []nfoDefaultMark `xml:"uniqueid"`
	}
	if xml.Unmarshal(data, &ids) != nil {
		return false
	}
	return slices.ContainsFunc(ids.UniqueIDs, func(id nfoDefaultMark) bool {
		return id.Default == "true"
	})
}

// The one attribute of a uniqueid element the default rule reads.
type nfoDefaultMark struct {
	Default string `xml:"default,attr"`
}
