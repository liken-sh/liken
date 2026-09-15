package main

// trailerrole.go is the trailer container's run: the titles it asks about,
// the providers it asks, and what it leaves in the ledger and the trailers
// table. It records ids and links. Nothing here plays or downloads a video.

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// The name of the container that runs the trailer group, which is one fact.
const trailerContainerName = "trailer"

// The answerers the trailer container asks, in the order the Library's
// sources name their blocks.
type trailerLine struct {
	answerers []trailerAnswerer
}

// The line is built in the order LIBRARY_SOURCES names the blocks. A block
// whose key or address did not reach the container is skipped with no error.
func newTrailerLine(blocks []string, value func(string) string) *trailerLine {
	line := &trailerLine{}
	for _, block := range blocks {
		switch block {
		case providerBlockTMDb:
			if token := value(providerTokenVariable(block)); token != "" {
				line.answerers = append(line.answerers,
					newTMDbTrailerAnswerer(newTMDbClient(tmdbAPIBase, token)))
			}
		case providerBlockPeerTube:
			if endpoint := value(peertubeEndpointVariable); endpoint != "" {
				line.answerers = append(line.answerers,
					newPeertubeTrailerAnswerer(newPeertubeClient(endpoint)))
			}
		}
	}
	return line
}

// One title's ask: every answerer, because the trailers of a title are the
// union of what the providers hold, never the first answer alone. A provider
// that is down leaves the other blocks their answer, so the error stands only
// where no block answered at all.
func (l *trailerLine) ask(ctx context.Context, title trailerTitle) ([]trailerEntry, []string, error) {
	var entries []trailerEntry
	var blocks []string
	var failure error
	for _, one := range l.answerers {
		held, err := one.trailers(ctx, title)
		if err != nil {
			if failure == nil {
				failure = err
			}
			continue
		}
		if len(held) == 0 {
			continue
		}
		entries = append(entries, held...)
		blocks = append(blocks, one.providerBlock())
	}
	if len(entries) == 0 {
		return nil, nil, failure
	}
	return entries, blocks, nil
}

// The line is built once for the container, so the settings a provider states
// are read once. A container with no answerer at all is a manifest to repair,
// because the operator creates it only where a source serves the fact.
func (e *enricher) trailerFact(ctx context.Context) error {
	if e.trailers == nil {
		e.trailers = newTrailerLine(commaNames(os.Getenv(librarySourcesVariable)), os.Getenv)
	}
	if len(e.trailers.answerers) == 0 {
		return fmt.Errorf("no provider key reached this container, and the %s fact cannot ask without one", factTrailer)
	}
	return e.trailerGap(ctx, e.trailers)
}

// A catalog read that fails ends the container, because the gap list is the
// work. One title that fails records an error attempt, and the run carries on
// to the next.
func (e *enricher) trailerGap(ctx context.Context, line *trailerLine) error {
	ids, err := e.gaps(ctx, factTrailer, time.Now().UTC())
	if err != nil {
		return err
	}
	found := 0
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
		if e.trailerOne(ctx, line, item) {
			found++
		}
	}
	e.logf("named the trailers of %d of the %d titles the gap held", found, len(ids))
	return nil
}

// One title's ask and the record of it. The whole list is replaced, because
// the ledger says which trailers the providers hold now.
func (e *enricher) trailerOne(ctx context.Context, line *trailerLine, item identityItem) bool {
	folder := filepath.Join(e.root, item.path)
	entries, blocks, err := line.ask(ctx, e.trailerTitle(item, folder))
	if err != nil {
		e.logf("could not read the trailers of %s: %v", item.id, err)
		e.recordTrailers(folder, nil, nil, attemptError)
		return false
	}
	if len(entries) == 0 {
		e.recordTrailers(folder, []trailerEntry{}, nil, attemptNothing)
		return false
	}
	e.recordTrailers(folder, sortedTrailers(entries), blocks, attemptFound)
	return true
}

// The ids a provider keys on come off the sidecar, where the identity fact
// wrote every one of them. The title and the year come off the catalog, for a
// provider keyed by search. A folder with no sidecar carries no id, which is
// not an error.
func (e *enricher) trailerTitle(item identityItem, folder string) trailerTitle {
	title := trailerTitle{kind: e.kind, title: item.title, year: item.year}
	sidecar, _ := identitySidecar(e.kind, folder)
	document, err := os.ReadFile(sidecar)
	if err != nil {
		return title
	}
	title.ids = sidecarIDs(document)
	return title
}

// The order a person reads the list in: the provider, then the score, highest
// first, then the provider's own key.
func sortedTrailers(entries []trailerEntry) []trailerEntry {
	sorted := slices.Clone(entries)
	slices.SortStableFunc(sorted, func(a, b trailerEntry) int {
		if order := cmp.Compare(a.Provider, b.Provider); order != 0 {
			return order
		}
		if order := cmp.Compare(b.Score, a.Score); order != 0 {
			return order
		}
		return cmp.Compare(a.Key, b.Key)
	})
	return sorted
}

// The list and the attempt are one write of one file, so a reader never sees
// an answer without its attempt. A provider that was down leaves the list as
// it is, because a failed ask says nothing about which trailers the title
// has.
func (e *enricher) recordTrailers(folder string, entries []trailerEntry, blocks []string, result string) {
	now := time.Now().UTC()
	err := e.writer.updateLikenLedger(folder, factTrailer, func(ledger *likenLedger) {
		if result != attemptError {
			ledger.Trailers = entries
		}
		ledger.noteAttempt(likenAttempt{
			Path: likenSelfPath, At: now, Result: result, Provider: blocks,
		})
	})
	if err != nil {
		e.logf("could not record the %s attempt at %s: %v",
			factTrailer, relativePath(e.root, folder), err)
	}
	e.writeRows(factTrailer, folder, result == attemptFound)
}

// The trailer gap reads no table of its own. Every identified title is asked
// again once its last attempt has passed that attempt's own window, because a
// provider gains trailers over time and drops them.
func trailerGapQuery() string {
	return `SELECT id FROM (` +
		`SELECT library, id FROM movies WHERE id NOT LIKE 'movie:path:%' ` +
		`UNION ALL SELECT library, id FROM series WHERE id NOT LIKE 'series:path:%') AS items ` +
		`WHERE library = ?1 AND (` + attemptClause(factTrailer, "id") +
		beforeReleaseClause(factTrailer, "id") + `)`
}
