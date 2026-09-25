package main

// imdbcredits.go is the credits fact's read of the IMDb datasets. The
// principal credits of every title are in title.principals, keyed by the
// title's IMDb id, and each names a person by an IMDb id alone. The names are
// in name.basics. The container reads the two files in sequence: principals
// first, keeping the rows of the titles in its gap, then name.basics for the
// people those rows name. It skips name.basics when every person already has
// a .contributors/ entry, because the entry holds the name.
//
// title.principals holds the billed cast and the key crew, about nine people
// for each title, and TMDb holds the full cast. So the imdb block answers the
// credits of a title only where no source before it in the Library's order
// answered, which the table states as a fallback fact of the block.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The dataset files the credits fact reads.
const (
	datasetTitlePrincipals = "title.principals"
	datasetNameBasics      = "name.basics"
)

// Which categories of title.principals become which part. actor, actress,
// and self are the cast. director and writer are the crew the credits model
// holds, as TMDb's credits give them. The other crew categories, such as
// producer and composer, have no part in the model, and archive footage is
// not a credit, so every other category is left out.
var principalParts = map[string]string{
	"actor":    creditPartActor,
	"actress":  creditPartActor,
	"self":     creditPartActor,
	"director": creditPartDirector,
	"writer":   creditPartWriter,
}

// One row of title.principals the read keeps.
type principal struct {
	ordering   int
	person     string
	part       string
	characters string
}

// What the credits read left: the IMDb id of every title it covers, and the
// credits of each IMDb id title.principals holds. done closes when the read
// ends, and err is why it ended early.
type creditReads struct {
	done    chan struct{}
	titles  map[string]string
	credits map[string]titleCredits
	err     error
	fetcher *datasetFetcher
}

func (r *creditReads) wait(ctx context.Context) error {
	select {
	case <-r.done:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// startCreditReads starts the read of a container that runs the credits fact
// with the imdb block among its sources. A gap with no title that has an IMDb
// id sends no request.
func (e *enricher) startCreditReads(ctx context.Context) {
	reads := &creditReads{done: make(chan struct{}), titles: map[string]string{},
		credits: map[string]titleCredits{}, fetcher: e.datasetFetcher()}
	e.credits = reads
	ids, err := e.gaps(ctx, factCredits, time.Now().UTC())
	if err == nil {
		reads.titles, err = e.creditTitles(ctx, ids)
	}
	if err != nil || len(reads.titles) == 0 {
		reads.err = err
		close(reads.done)
		return
	}
	go func() {
		defer close(reads.done)
		if reads.err = e.readCredits(ctx, reads); reads.err != nil {
			e.logf("could not read the IMDb datasets, so this run leaves the credits gap to the other sources: %v",
				reads.err)
		}
	}()
}

// coverCreditGap reads the two files again for the titles of a pass that the
// reads so far do not cover, as coverDatasetGap does for the rating.
func (e *enricher) coverCreditGap(ctx context.Context, ids []string) {
	reads := e.credits
	if reads == nil || reads.wait(ctx) != nil {
		return
	}
	var missing []string
	for _, id := range ids {
		if _, held := reads.titles[id]; !held {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return
	}
	titles, err := e.creditTitles(ctx, missing)
	batch := &creditReads{titles: titles, credits: map[string]titleCredits{}, fetcher: reads.fetcher}
	if err == nil {
		err = e.readCredits(ctx, batch)
	}
	if err != nil {
		e.logf("could not read the IMDb datasets, so this run leaves the credits gap to the other sources: %v", err)
		reads.err = err
		return
	}
	maps.Copy(reads.titles, batch.titles)
	maps.Copy(reads.credits, batch.credits)
}

// The IMDb id of each title of the gap, from the catalog's aliases. A title
// with no IMDb id is covered with an empty id, so a later pass reads nothing
// for it.
func (e *enricher) creditTitles(ctx context.Context, ids []string) (map[string]string, error) {
	aliases, err := e.imdbAliases(ctx)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	for _, id := range ids {
		titles[id] = aliases[id]
	}
	return titles, nil
}

// The two reads, in sequence, for the titles of one batch.
func (e *enricher) readCredits(ctx context.Context, reads *creditReads) error {
	wanted := map[string]bool{}
	for _, tconst := range reads.titles {
		if tconst != "" {
			wanted[tconst] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	started := time.Now()
	rows := map[string][]principal{}
	if _, err := reads.fetcher.read(ctx, datasetTitlePrincipals, func(cells [][]byte) {
		tconst := datasetCell(cells, 0)
		part := principalParts[datasetCell(cells, 3)]
		if !wanted[tconst] || part == "" {
			return
		}
		ordering, _ := strconv.Atoi(datasetCell(cells, 1))
		rows[tconst] = append(rows[tconst], principal{ordering: ordering, person: datasetCell(cells, 2),
			part: part, characters: datasetCell(cells, 5)})
	}); err != nil {
		return fmt.Errorf("reading %s: %w", datasetTitlePrincipals, err)
	}
	e.logf("read %s in %s and kept the credits of %d of the %d titles the gap asks for",
		datasetTitlePrincipals, time.Since(started).Round(time.Millisecond), len(rows), len(wanted))
	names, err := e.principalNames(ctx, reads.fetcher, rows)
	if err != nil {
		return err
	}
	for tconst, held := range rows {
		reads.credits[tconst] = principalCredits(held, names)
	}
	return nil
}

// The name of every person the rows name: from the person's .contributors/
// entry where the store holds one, and from name.basics for the rest. A run
// whose people all have entries does not read name.basics.
func (e *enricher) principalNames(ctx context.Context, fetcher *datasetFetcher,
	rows map[string][]principal) (map[string]string, error) {
	names := map[string]string{}
	unnamed := map[string]bool{}
	for _, held := range rows {
		for _, row := range held {
			if _, done := names[row.person]; done || unnamed[row.person] {
				continue
			}
			if name := e.entryName(row.person); name != "" {
				names[row.person] = name
			} else {
				unnamed[row.person] = true
			}
		}
	}
	if len(unnamed) == 0 {
		return names, nil
	}
	started := time.Now()
	if _, err := fetcher.read(ctx, datasetNameBasics, func(cells [][]byte) {
		if person := datasetCell(cells, 0); unnamed[person] {
			names[person] = strings.TrimSpace(datasetCell(cells, 1))
		}
	}); err != nil {
		return nil, fmt.Errorf("reading %s: %w", datasetNameBasics, err)
	}
	e.logf("read %s in %s for the names of %d people", datasetNameBasics,
		time.Since(started).Round(time.Millisecond), len(unnamed))
	return names, nil
}

// The name in the entry that plan 65's join finds for one IMDb id, or an
// empty string where the store holds no entry for it.
func (e *enricher) entryName(person string) string {
	directory := e.contributorByID(providerIDs{contributorIMDbScheme: person})
	if directory == "" {
		return ""
	}
	held, _, err := readContributorFile(filepath.Join(e.root, directory, contributorFileName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(held.Name)
}

// One title's credits as TMDb's credits give them: the cast in IMDb's
// ordering with the characters as the role, then the directors and the
// writers in that same ordering, one entry for each person. A person with no
// name is left out, because a credit names a person.
func principalCredits(rows []principal, names map[string]string) titleCredits {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ordering < rows[j].ordering })
	credits := titleCredits{}
	held := map[string]map[string]bool{}
	for _, row := range rows {
		name := names[row.person]
		if name == "" || held[row.part][row.person] {
			continue
		}
		if held[row.part] == nil {
			held[row.part] = map[string]bool{}
		}
		held[row.part][row.person] = true
		ids := providerIDs{contributorIMDbScheme: row.person}
		switch row.part {
		case creditPartActor:
			credits.Cast = append(credits.Cast, creditedActor{Name: name, Role: principalRole(row.characters),
				Order: len(credits.Cast), IDs: ids})
		case creditPartDirector:
			credits.Directors = append(credits.Directors, creditedPerson{Name: name, IDs: ids})
		case creditPartWriter:
			credits.Writers = append(credits.Writers, creditedPerson{Name: name, IDs: ids})
		}
	}
	return credits
}

// IMDb writes the characters as a JSON list, and one actor can play several.
// The role is the list joined with a slash, the form Jellyfin writes for an
// actor of two parts.
func principalRole(characters string) string {
	var list []string
	if characters == "" || json.Unmarshal([]byte(characters), &list) != nil {
		return ""
	}
	return strings.Join(slices.DeleteFunc(list, func(one string) bool { return strings.TrimSpace(one) == "" }), " / ")
}

// The IMDb id the catalog's aliases hold for every item of the library.
func (e *enricher) imdbAliases(ctx context.Context) (map[string]string, error) {
	aliases := map[string]string{}
	err := e.catalog.stream(ctx, `SELECT item, alias FROM aliases WHERE library = ? AND alias LIKE '%:imdb:%'`,
		[]any{e.library}, func(cells []any) error {
			item, _ := cells[0].(string)
			alias, _ := cells[1].(string)
			aliases[item] = alias[strings.LastIndex(alias, ":")+1:]
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("reading the IMDb ids of %s: %w", e.library, err)
	}
	return aliases, nil
}

// Whether a container starts the dataset reads of one fact: the container
// runs the fact, and LIBRARY_SOURCES names the imdb block.
func datasetFactRuns(facts []string, fact string) bool {
	return slices.Contains(facts, fact) &&
		slices.Contains(commaNames(os.Getenv(librarySourcesVariable)), providerBlockIMDb)
}
