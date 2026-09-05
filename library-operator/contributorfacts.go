package main

// The people phase. The three facts that fill one person's entry in
// .contributors/, the gap query each of them works from, and the container
// that runs them. Every fact keys on the person's own directory, so its
// ledger, its attempt, and its files sit together under that directory.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The facts the contributors container names in LIBRARY_FACTS, in the order it
// runs them. The ids run first, because the ids are what a later provider of a
// biography or a headshot keys on.
var contributorFactNames = []string{factContributorIDs, factContributorBiography, factContributorHeadshot}

// The name of the container that runs the people facts.
const contributorsContainerName = "contributors"

// The provider one Library's sources hold for its people: the first of them
// that is Ready and serves any contributor fact.
func (s providerSet) servingContributors(namespace string, sources []string) *MetadataProvider {
	return s.servingAny(namespace, sources, contributorFactNames)
}

// One fact's run, bound to its name, so every contributor fact runs the same
// loop over its own gap.
func contributorFactRun(fact string) factRun {
	return func(ctx context.Context, e *enricher) error { return e.contributorFact(ctx, fact) }
}

// A container with no key fails before it writes anything, so the Job says
// what the pod is missing.
func (e *enricher) contributorFact(ctx context.Context, fact string) error {
	token := os.Getenv(tmdbTokenVariable)
	if token == "" {
		return fmt.Errorf("%s is empty, and the %s fact cannot ask a provider without it",
			tmdbTokenVariable, fact)
	}
	return e.contributorGap(ctx, fact, newTMDbClient(tmdbAPIBase, token))
}

// A catalog read that fails ends the container, because the gap list is the
// work. One person the provider refuses records an error attempt, and the run
// carries on to the next.
func (e *enricher) contributorGap(ctx context.Context, fact string, client *tmdbClient) error {
	gaps, err := e.catalog.contributorGaps(ctx, e.library, fact, time.Now().UTC(), e.refresh[fact])
	if err != nil {
		return err
	}
	written := 0
	for _, gap := range gaps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !e.inScope(gap.path) {
			continue
		}
		if e.fillContributor(ctx, client, fact, gap) {
			written++
		}
	}
	e.logf("wrote the %s of %d of the %d people that lacked it", fact, written, len(gaps))
	return nil
}

// One person's gap: the directory the person's files sit in, relative to the
// library root, and the TMDb id the calls key on.
type contributorGap struct {
	path string
	tmdb string
}

func (e *enricher) fillContributor(ctx context.Context, client *tmdbClient, fact string, gap contributorGap) bool {
	folder := filepath.Join(e.root, gap.path)
	switch fact {
	case factContributorIDs:
		return e.fillContributorIDs(ctx, client, folder, gap)
	case factContributorBiography:
		return e.fillContributorBiography(ctx, client, folder, gap)
	case factContributorHeadshot:
		return e.fillContributorHeadshot(ctx, client, folder, gap)
	}
	return false
}

// The ids fact. One entry of a YAML file cannot be edited in place, so the
// whole file is read, changed, and written again through the write door. The
// hash the ledger keeps is what makes that safe: a file whose bytes are not
// the ones this fact left is a file a person edited, and the fact stops for
// that person and says so.
func (e *enricher) fillContributorIDs(ctx context.Context, client *tmdbClient,
	folder string, gap contributorGap) bool {
	held, data, err := readContributorFile(filepath.Join(folder, contributorFileName))
	if err != nil {
		e.logf("could not read the entry of %s: %v", gap.path, err)
		e.recordContributor(folder, factContributorIDs, "", attemptError, "")
		return false
	}
	if data == nil {
		e.logf("the entry of %s is not on the volume", gap.path)
		e.recordContributor(folder, factContributorIDs, "", attemptError, "")
		return false
	}
	fought, err := e.contributorHeldByAnother(folder, data)
	if err != nil {
		e.logf("could not read the ledger of %s: %v", gap.path, err)
		e.recordContributor(folder, factContributorIDs, "", attemptError, "")
		return false
	}
	if fought {
		e.logf("another writer holds the entry of %s, so this run left it", gap.path)
		e.recordContributor(folder, factContributorIDs, "", attemptFight, "")
		return false
	}

	person, err := client.person(ctx, gap.tmdb)
	if err != nil {
		e.logf("could not read the person of %s: %v", gap.path, err)
		e.recordContributor(folder, factContributorIDs, "", attemptError, "")
		return false
	}
	ids, err := client.personIDs(ctx, gap.tmdb)
	if err != nil {
		e.logf("could not read the ids of %s: %v", gap.path, err)
		e.recordContributor(folder, factContributorIDs, "", attemptError, "")
		return false
	}
	return e.writeContributorIDs(folder, gap, held, data, person, ids)
}

// The write. An id the file already carries stands, because the ids of a
// person are a set every provider adds to, and a date the provider does not
// hold leaves the one the file has. A file the provider's answer does not
// change is left as it is, and the ledger still records the answer.
func (e *enricher) writeContributorIDs(folder string, gap contributorGap,
	held contributorFile, data []byte, person tmdbPerson, ids providerIDs) bool {
	if len(ids) == 0 && person.Birthday == "" && person.Deathday == "" {
		e.logf("the provider holds no ids or dates for %s", gap.path)
		e.recordContributor(folder, factContributorIDs, "", attemptNothing, "")
		return false
	}
	written := marshalContributorFile(filledContributor(held, gap.tmdb, person, ids))
	if bytes.Equal(written, data) {
		e.recordContributor(folder, factContributorIDs, providerBlockTMDb, attemptFound, contentHash(written))
		return false
	}
	if err := e.writer.write(filepath.Join(folder, contributorFileName), written); err != nil {
		e.logf("could not write the entry of %s: %v", gap.path, err)
		e.recordContributor(folder, factContributorIDs, "", attemptError, "")
		return false
	}
	e.logf("wrote the ids of %s from %s", gap.path, providerBlockTMDb)
	e.recordContributor(folder, factContributorIDs, providerBlockTMDb, attemptFound, contentHash(written))
	return true
}

// The entry the ids fact leaves: every scheme the file and the provider hold
// together, with the file's own id winning where both name one, and the dates
// the provider stated.
func filledContributor(held contributorFile, tmdb string, person tmdbPerson, ids providerIDs) contributorFile {
	filled := held
	filled.IDs = providerIDs{contributorTMDbScheme: tmdb}
	for scheme, id := range ids {
		filled.IDs[scheme] = id
	}
	for scheme, id := range held.IDs {
		if id != "" {
			filled.IDs[scheme] = id
		}
	}
	if born := strings.TrimSpace(person.Birthday); born != "" {
		filled.Born = born
	}
	if died := strings.TrimSpace(person.Deathday); died != "" {
		filled.Died = died
	}
	return filled
}

// The fight check. The ledger holds the hash of the file this fact last left,
// and a fact with no entry in its ledger has written nothing yet, so whatever
// the file holds is the credits fact's own and this fact takes it over.
func (e *enricher) contributorHeldByAnother(folder string, data []byte) (bool, error) {
	ledger, err := readLikenLedger(folder, factContributorIDs)
	if err != nil {
		return false, err
	}
	held, wrote := ledger.itemAt(likenSelfPath)
	if !wrote || held.Wrote == "" {
		return false, nil
	}
	return contentHash(data) != held.Wrote, nil
}

// The biography fact. The text lands beside the entry where no file of that
// name exists, so a biography a person wrote by hand stays.
func (e *enricher) fillContributorBiography(ctx context.Context, client *tmdbClient,
	folder string, gap contributorGap) bool {
	if e.contributorFileHeld(folder, contributorBiographyName, factContributorBiography, gap) {
		return false
	}
	person, err := client.person(ctx, gap.tmdb)
	if err != nil {
		e.logf("could not read the person of %s: %v", gap.path, err)
		e.recordContributor(folder, factContributorBiography, "", attemptError, "")
		return false
	}
	text := strings.TrimSpace(person.Biography)
	if text == "" {
		e.logf("the provider holds no biography of %s", gap.path)
		e.recordContributor(folder, factContributorBiography, "", attemptNothing, "")
		return false
	}
	return e.createContributorFile(folder, contributorBiographyName,
		factContributorBiography, gap, []byte(text+"\n"))
}

// The headshot fact. The image is downloaded and created where no file of that
// name exists, the way an art fact writes a poster.
func (e *enricher) fillContributorHeadshot(ctx context.Context, client *tmdbClient,
	folder string, gap contributorGap) bool {
	if e.contributorFileHeld(folder, contributorHeadshotName, factContributorHeadshot, gap) {
		return false
	}
	person, err := client.person(ctx, gap.tmdb)
	if err != nil {
		e.logf("could not read the person of %s: %v", gap.path, err)
		e.recordContributor(folder, factContributorHeadshot, "", attemptError, "")
		return false
	}
	address := tmdbImageURL(tmdbHeadshotSize, person.ProfilePath)
	if address == "" {
		e.logf("the provider holds no headshot of %s", gap.path)
		e.recordContributor(folder, factContributorHeadshot, "", attemptNothing, "")
		return false
	}
	data, err := client.fetchFile(ctx, address)
	if err != nil {
		e.logf("could not read %s: %v", address, err)
		e.recordContributor(folder, factContributorHeadshot, "", attemptError, "")
		return false
	}
	return e.createContributorFile(folder, contributorHeadshotName, factContributorHeadshot, gap, data)
}

// The volume is read before the provider is asked, because a file that landed
// since the last walk is the answer already and costs no call. The ledger
// records that the file was already there.
func (e *enricher) contributorFileHeld(folder, name, fact string, gap contributorGap) bool {
	held, err := fileExists(filepath.Join(folder, name))
	if err != nil {
		e.logf("could not read the %s of %s: %v", name, gap.path, err)
		e.recordContributor(folder, fact, "", attemptError, "")
		return true
	}
	if held {
		e.recordContributor(folder, fact, artProviderExisting, attemptFound, "")
	}
	return held
}

// The create that never lands on a file that exists. A file that arrived
// between the read and the write is kept, and the ledger says so.
func (e *enricher) createContributorFile(folder, name, fact string, gap contributorGap, data []byte) bool {
	written, err := e.writer.createInto(folder, name, data)
	if err != nil {
		e.logf("could not write the %s of %s: %v", name, gap.path, err)
		e.recordContributor(folder, fact, "", attemptError, "")
		return false
	}
	if !written {
		e.recordContributor(folder, fact, artProviderExisting, attemptFound, "")
		return false
	}
	e.logf("wrote the %s of %s from %s", name, gap.path, providerBlockTMDb)
	e.recordContributor(folder, fact, providerBlockTMDb, attemptFound, "")
	return true
}

// The item entry and the attempt are one write of one file, as every other
// fact records them, so a reader never sees an answer without its attempt. A
// person's ledger sits in the .liken directory of the person's own directory,
// and its one entry is the person.
func (e *enricher) recordContributor(folder, fact, provider, result, wrote string) {
	now := time.Now().UTC()
	err := e.writer.updateLikenLedger(folder, fact, func(ledger *likenLedger) {
		if provider != "" {
			item := likenItem{Path: likenSelfPath, Provider: providerNames{provider}, Wrote: wrote}
			if provider != artProviderExisting {
				item.Written = now
			}
			ledger.noteItem(item)
		}
		ledger.noteAttempt(likenAttempt{Path: likenSelfPath, At: now, Result: result})
	})
	if err != nil {
		e.logf("could not record the %s attempt at %s: %v", fact, folder, err)
	}
	e.writeRows(fact, folder, result == attemptFound)
}

// One fact's work list, out of the local copy of the catalog, with the same
// query the reporter counts the gap with. Every row names the person's
// directory and the TMDb id to ask for.
func (c *Catalog) contributorGaps(ctx context.Context, library, fact string,
	now, refresh time.Time) ([]contributorGap, error) {
	var gaps []contributorGap
	err := c.stream(ctx, gapQueries[fact], gapParams(fact, library, now, refresh), func(cells []any) error {
		if len(cells) < 2 {
			return nil
		}
		path, _ := cells[0].(string)
		id, _ := cells[1].(string)
		if path == "" || id == "" {
			return nil
		}
		gaps = append(gaps, contributorGap{path: path, tmdb: id})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the %s gap of %s: %w", fact, library, err)
	}
	return gaps, nil
}

// The gap query of one contributor fact. A person with no TMDb id is no gap,
// because every call this image makes keys on that id, and the join is what
// reads it. The query excludes a person with an attempt inside that attempt's
// own window.
func contributorGapSQL(fact, condition string) string {
	return `SELECT c.path, a.id FROM contributors AS c ` +
		`JOIN contributor_aliases AS a ON a.library = c.library AND a.path = c.path ` +
		`AND a.scheme = '` + contributorTMDbScheme + `' ` +
		`WHERE c.library = ?1 AND ` + gapClause(fact, "c.path", condition)
}

// The ids gap: a person with no birth date, or with no id under any scheme but
// TMDb's own. Both are what the ids fact fills, and either one alone is work.
func contributorIDsGapSQL() string {
	return contributorGapSQL(factContributorIDs,
		`c.born = '' OR NOT EXISTS (SELECT 1 FROM contributor_aliases AS o `+
			`WHERE o.library = c.library AND o.path = c.path `+
			`AND o.scheme != '`+contributorTMDbScheme+`')`)
}

// The gap of a fact that writes one file: the column the scanner sets where
// the file is beside the entry.
func contributorFileGapSQL(fact, column string) string {
	return contributorGapSQL(fact, `c.`+column+` = 0`)
}
