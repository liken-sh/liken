package main

// identityrole.go is the identity fact's container: what it reads out of
// the catalog for one gap, and what it writes when the ladder answers.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// A container with no key fails before it writes anything, so the Job says
// what the pod is missing.
func (e *enricher) identityFact(ctx context.Context) error {
	token := os.Getenv(tmdbTokenVariable)
	if token == "" {
		return fmt.Errorf("%s is empty, and the identity fact cannot ask a provider without it", tmdbTokenVariable)
	}
	client := newTMDbClient(tmdbAPIBase, token, e.providerTransport)
	client.recordTo(e.tallies)
	return e.identityGap(ctx, client)
}

// A catalog read that fails ends the container, because the gap list is the
// work. A provider that refuses one title records an error attempt, and the
// run carries on to the next.
func (e *enricher) identityGap(ctx context.Context, client *tmdbClient) error {
	ids, err := e.gaps(ctx, factIdentity, time.Now().UTC())
	if err != nil {
		return err
	}
	asked := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		item, held, err := e.catalog.identityItem(ctx, e.library, id)
		if err != nil {
			return err
		}
		if !held || !e.inScope(item.path) {
			continue
		}
		heldIDs, err := e.catalog.titleIDs(ctx, e.library, id)
		if err != nil {
			return err
		}
		if e.holdsAnID(heldIDs) {
			e.fillIDs(ctx, client, item, heldIDs)
		} else {
			e.identifyOne(ctx, client, item)
		}
		asked++
	}
	e.logf("asked the provider about %d of the %d titles missing an id", asked, len(ids))
	return nil
}

// One title: climb the ladder, write the id into the .nfo file where the ladder
// is sure, and record the answer in the ledger either way.
func (e *enricher) identifyOne(ctx context.Context, client *tmdbClient, item identityItem) {
	folder := filepath.Join(e.root, item.path)
	answer, err := climbIdentityLadder(ctx, client, identitySearch{
		kind:     e.kind,
		title:    item.title,
		year:     item.year,
		duration: e.runtimeOf(item, folder),
		episodes: e.episodeClues(folder),
	})
	if err != nil {
		e.logf("could not identify %s: %v", opaqueID(item.id), err)
		e.recordIdentity(folder, nil, attemptError)
		return
	}
	switch {
	case answer.id > 0:
		e.writeIdentity(ctx, client, folder, item, answer)
	case len(answer.candidates) > 0:
		e.logf("%s waits for a person, with %d candidates", opaqueID(item.id), len(answer.candidates))
		e.recordIdentity(folder, &likenItem{Path: likenSelfPath, Candidates: answer.candidates}, attemptCandidates)
	default:
		e.logf("no provider named %s", opaqueID(item.id))
		e.recordIdentity(folder, nil, attemptNothing)
	}
}

// The ladder's TMDb id goes in first, and the other databases' ids follow it.
func (e *enricher) writeIdentity(ctx context.Context, client *tmdbClient, folder string,
	item identityItem, answer identityAnswer) {
	id := strconv.Itoa(answer.id)
	nfoPath, rootElement := identityNFO(e.kind, folder)
	if err := e.writeUniqueID(nfoPath, rootElement, item.title, "tmdb", id); err != nil {
		e.logf("could not write the id of %s: %v", opaqueID(item.id), err)
		e.recordIdentity(folder, nil, attemptError)
		return
	}
	e.logf("identified %s as tmdb %s, by %s", opaqueID(item.id), id, answer.reason)
	e.writeOtherIDs(ctx, client, folder, item, providerIDs{"tmdb": id}, answer.reason, true)
}

// The item entry and the attempt are one write of one file, so a reader never
// sees an answer without its attempt.
func (e *enricher) recordIdentity(folder string, entry *likenItem, result string) {
	e.tallies.add(tallyAttempts, 1, "fact", factIdentity, "result", result)
	err := e.writer.updateLikenLedger(folder, factIdentity, func(ledger *likenLedger) {
		if entry != nil {
			ledger.noteItem(*entry)
		}
		ledger.noteAttempt(likenAttempt{Path: likenSelfPath, At: time.Now().UTC(), Result: result})
	})
	if err != nil {
		e.logf("could not record the identity attempt at %s: %v", e.named(folder), err)
	}
	if result == attemptFound || result == attemptPartial {
		e.rescanTitle(folder)
		return
	}
	e.writeRows(factIdentity, folder, false)
}

// Which .nfo file holds a title's id: tvshow.nfo for a series, movie.nfo for
// a movie.
func identityNFO(kind, folder string) (string, string) {
	if kind == libraryKindSeries {
		return filepath.Join(folder, seriesNFOName), nfoRootSeries
	}
	return filepath.Join(folder, movieNFOName), nfoRootMovie
}

// The runtime comes from the .nfo file where the catalog has none, because the
// probe container wrote it in this same Job and no scan has read it yet.
func (e *enricher) runtimeOf(item identityItem, folder string) time.Duration {
	if item.duration > 0 {
		return time.Duration(item.duration) * time.Second
	}
	if e.kind != libraryKindMovies {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(folder, movieNFOName))
	if err != nil {
		return 0
	}
	meta, err := parseMovieNFO(data)
	if err != nil {
		return 0
	}
	return time.Duration(meta.Duration) * time.Second
}

// What the identity fact reads for one gap: where the title sits, the
// clues its name gave, and the runtime the catalog holds.
type identityItem struct {
	id       string
	path     string
	title    string
	year     int
	duration int64
}

// The item table follows the id's own scope, movie or series. A row that left
// between the gap read and this one is skipped and not an error, because a
// folder may move while a Job runs.
func (c *Catalog) identityItem(ctx context.Context, library, id string) (identityItem, bool, error) {
	table := "movies"
	if !isMovieID(id) {
		table = "series"
	}
	item := identityItem{id: id}
	held := false
	err := c.stream(ctx, `SELECT path, title, released, duration FROM `+table+` WHERE library = ? AND id = ?`,
		[]any{library, id}, func(cells []any) error {
			if held || len(cells) < 4 {
				return nil
			}
			held = true
			item.path, _ = cells[0].(string)
			item.title, _ = cells[1].(string)
			released, _ := cells[2].(string)
			item.year = leadingYear(released)
			item.duration = cellNumber(cells[3])
			return nil
		})
	return item, held, err
}

func isMovieID(id string) bool {
	return len(id) > len(scopeMovie) && id[:len(scopeMovie)+1] == scopeMovie+":"
}
