package main

// PROSE: this file is the identity concern's container: what it reads out of
// the catalog for one gap, and what it writes when the ladder answers.

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// PROSE: the role's whole program. A container with no key fails before it
// writes anything, so the Job says what the pod is missing.
func runIdentity() {
	stopped, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	work := newEnricher(os.Stdout)
	token := os.Getenv(tmdbTokenVariable)
	if token == "" {
		work.logf("%s is empty, and the identity concern cannot ask a provider without it", tmdbTokenVariable)
		stop()
		os.Exit(1)
	}
	if err := work.identityGap(stopped, newTMDbClient(tmdbAPIBase, token)); err != nil {
		work.logf("the identity container failed: %v", err)
		stop()
		os.Exit(1)
	}
}

// PROSE: says why a catalog read that fails ends the container, and why a
// provider that refuses one title records an error attempt and the run carries
// on to the next.
func (e *enricher) identityGap(ctx context.Context, client *tmdbClient) error {
	ids, err := e.gaps(ctx, concernIdentity, time.Now().UTC())
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
		e.identifyOne(ctx, client, item)
		asked++
	}
	e.logf("asked the provider about %d of the %d titles with no id", asked, len(ids))
	return nil
}

// PROSE: one title: climb the ladder, write the id into the sidecar where the
// ladder is sure, and record the answer in the ledger either way.
func (e *enricher) identifyOne(ctx context.Context, client *tmdbClient, item identityItem) {
	folder := filepath.Join(e.root, item.path)
	answer, err := climbIdentityLadder(ctx, client, identitySearch{
		kind:     e.kind,
		title:    item.title,
		year:     item.year,
		duration: e.runtimeOf(item, folder),
	})
	if err != nil {
		e.logf("could not identify %s: %v", item.path, err)
		e.recordIdentity(folder, nil, attemptError)
		return
	}
	switch {
	case answer.id > 0:
		e.writeIdentity(folder, item, answer)
	case len(answer.candidates) > 0:
		e.logf("%s waits for a person, with %d candidates", item.path, len(answer.candidates))
		e.recordIdentity(folder, &likenItem{Path: likenSelfPath, Candidates: answer.candidates}, attemptCandidates)
	default:
		e.logf("no provider named %s", item.path)
		e.recordIdentity(folder, nil, attemptNothing)
	}
}

// PROSE: says why the id goes into the .nfo and the reason into the ledger: the
// sidecar is what the next scan reads, and the ledger is never the truth.
func (e *enricher) writeIdentity(folder string, item identityItem, answer identityAnswer) {
	id := strconv.Itoa(answer.id)
	element := fmt.Appendf(nil, `<uniqueid type="tmdb" default="true">%s</uniqueid>`, id)
	sidecar, rootElement := identitySidecar(e.kind, folder)

	err := e.writer.editNFO(sidecar, rootElement, item.title,
		xmlElement{name: "uniqueid", attribute: "type", value: "tmdb"}, element)
	if err != nil {
		e.logf("could not write the id of %s: %v", item.path, err)
		e.recordIdentity(folder, nil, attemptError)
		return
	}
	e.logf("identified %s as tmdb %s, by %s", item.path, id, answer.reason)
	e.recordIdentity(folder, &likenItem{
		Path: likenSelfPath, ID: providerIDs{"tmdb": id}, Reason: answer.reason, Written: time.Now().UTC(),
	}, attemptFound)
}

// PROSE: says why the item entry and the attempt are one write of one file.
func (e *enricher) recordIdentity(folder string, entry *likenItem, result string) {
	err := e.writer.updateLikenLedger(folder, concernIdentity, func(ledger *likenLedger) {
		if entry != nil {
			ledger.noteItem(*entry)
		}
		ledger.noteAttempt(likenAttempt{Path: likenSelfPath, At: time.Now().UTC(), Result: result})
	})
	if err != nil {
		e.logf("could not record the identity attempt at %s: %v", folder, err)
	}
}

// PROSE: says which sidecar carries a title's id.
func identitySidecar(kind, folder string) (string, string) {
	if kind == libraryKindSeries {
		return filepath.Join(folder, seriesSidecarName), nfoRootSeries
	}
	return filepath.Join(folder, movieSidecarName), nfoRootMovie
}

// PROSE: says why the runtime comes off the sidecar where the catalog has none:
// the probe container wrote it in this same Job, and no scan has read it yet.
func (e *enricher) runtimeOf(item identityItem, folder string) time.Duration {
	if item.duration > 0 {
		return time.Duration(item.duration) * time.Second
	}
	if e.kind != libraryKindMovies {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(folder, movieSidecarName))
	if err != nil {
		return 0
	}
	meta, err := parseMovieNFO(data)
	if err != nil {
		return 0
	}
	return time.Duration(meta.Duration) * time.Second
}

// PROSE: what the identity concern reads for one gap: where the title sits, the
// clues its name gave, and the runtime the catalog holds.
type identityItem struct {
	id       string
	path     string
	title    string
	year     int
	duration int64
}

// PROSE: says why the item table follows the id's own scope, and why a row that
// left between the gap read and this one is skipped rather than an error.
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
