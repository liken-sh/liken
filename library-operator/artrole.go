package main

// The art container's run: what it reads out of the catalog for one art fact,
// how it answers a file that already exists, and what it writes where none
// does. An art fact never opens a file another tool wrote, so a poster
// Jellyfin wrote stays as it is, and the ledger records that the file was
// already there.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The line is built once for the container, so the settings one provider
// states are read once for every art fact the container runs. A container with
// no answerer at all is a manifest to repair, because the operator creates it
// only where a source serves one of its facts.
func (e *enricher) artFact(ctx context.Context, fact string) error {
	if e.art == nil {
		e.art = newArtLine(commaNames(os.Getenv(librarySourcesVariable)), os.Getenv)
	}
	if len(e.art.answerers) == 0 {
		return fmt.Errorf("no provider key reached this container, and the %s fact cannot ask without one", fact)
	}
	return e.artGap(ctx, fact, e.art)
}

// A catalog read that fails ends the container, because the gap list is the
// work. A fact no answerer in the line serves reads no gap at all, so a
// Library whose sources hold one provider costs nothing for the art that
// provider does not serve. A provider that refuses one image records an error
// attempt, and the run carries on to the next.
func (e *enricher) artGap(ctx context.Context, fact string, line *artLine) error {
	if !line.live(fact) {
		return nil
	}
	gaps, err := e.catalog.artGaps(ctx, e.library, fact, time.Now().UTC(), e.refresh[fact])
	if err != nil {
		return err
	}
	written := 0
	for _, gap := range gaps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !e.inScope(gap.key) {
			continue
		}
		if e.artOne(ctx, line, artTypes[fact], gap) {
			written++
		}
	}
	e.logf("wrote %d of the %d %s files the library had none of", written, len(gaps), fact)
	return nil
}

// One gap. The volume is read before the provider is asked, because a file
// that landed since the last walk is the answer already and costs no call.
// Then one image is chosen, downloaded, and created, and the ledger records
// which provider answered.
func (e *enricher) artOne(ctx context.Context, line *artLine, art artType, gap artGap) bool {
	folder := filepath.Join(e.root, gap.folder())
	target := filepath.Join(folder, art.fileFor(gap))
	if held, err := fileExists(target); err != nil {
		e.logf("could not read %s: %v", target, err)
		e.recordArt(folder, art.fact, gap.entry(), "", attemptError)
		return false
	} else if held {
		e.recordArt(folder, art.fact, gap.entry(), artProviderExisting, attemptFound)
		return false
	}

	answerer, candidates, err := line.ask(ctx, art.fact, gap, e.artTitle(gap))
	if err != nil {
		e.logf("could not read the %s of %s: %v", art.fact, gap.key, err)
		e.recordArt(folder, art.fact, gap.entry(), "", attemptError)
		return false
	}
	image, held := chooseArt(candidates, artLanguage)
	if answerer == nil || !held {
		e.logf("no provider holds the %s of %s", art.fact, gap.key)
		e.recordArt(folder, art.fact, gap.entry(), "", attemptNothing)
		return false
	}
	return e.writeArt(ctx, answerer, art, gap, folder, target, image)
}

// The ids a fact asks with come off the sidecar itself, which is where the
// identity fact wrote every one of them. The art phase reads them because the
// gap carries the TMDb id alone and two of the three providers key on another
// id. A folder with no sidecar carries no id, which leaves those two providers
// no answer and is not an error.
func (e *enricher) artTitle(gap artGap) titleRef {
	sidecar, _ := identitySidecar(e.kind, filepath.Join(e.root, gap.folder()))
	document, err := os.ReadFile(sidecar)
	if err != nil {
		return titleRef{kind: e.kind}
	}
	return titleRef{kind: e.kind, ids: sidecarIDs(document)}
}

// The download and the write. The bytes live from the answer to the rename
// and no longer. A create that finds the file there answers as the read above
// does, because another writer reached it first.
func (e *enricher) writeArt(ctx context.Context, answerer artAnswerer, art artType, gap artGap,
	folder, target string, image artCandidate) bool {
	data, err := answerer.fetchFile(ctx, image.URL)
	if err != nil {
		e.logf("could not read %s: %v", image.URL, err)
		e.recordArt(folder, art.fact, gap.entry(), "", attemptError)
		return false
	}
	written, err := e.writer.createOnce(target, data)
	if err != nil {
		e.logf("could not write %s: %v", target, err)
		e.recordArt(folder, art.fact, gap.entry(), "", attemptError)
		return false
	}
	if !written {
		e.recordArt(folder, art.fact, gap.entry(), artProviderExisting, attemptFound)
		return false
	}
	e.logf("wrote the %s of %s from %s", art.fact, gap.key, answerer.providerBlock())
	e.recordArt(folder, art.fact, gap.entry(), answerer.providerBlock(), attemptFound)
	return true
}

// The item entry and the attempt are one write of one file, so a reader never
// sees an answer without its attempt. A provider name of nothing is a miss or
// an error, which the attempt itself states.
func (e *enricher) recordArt(folder, fact, entry, provider, result string) {
	now := time.Now().UTC()
	err := e.writer.updateLikenLedger(folder, fact, func(ledger *likenLedger) {
		if provider != "" {
			item := likenItem{Path: entry, Provider: providerNames{provider}}
			if provider != artProviderExisting {
				item.Written = now
			}
			ledger.noteItem(item)
		}
		ledger.noteAttempt(likenAttempt{Path: entry, At: now, Result: result})
	})
	if err != nil {
		e.logf("could not record the %s attempt at %s: %v", fact, entry, err)
	}
	e.writeRows(fact, folder, result == attemptFound)
}

// One art fact's work list, out of the local copy of the catalog, with the
// same query the reporter counts the gap with. Every row names the file to
// write, the TMDb id to ask for, and the season and episode where the fact
// needs them.
func (c *Catalog) artGaps(ctx context.Context, library, fact string,
	now, refresh time.Time) ([]artGap, error) {
	var gaps []artGap
	err := c.stream(ctx, gapQueries[fact], gapParams(fact, library, now, refresh), func(cells []any) error {
		if len(cells) < 4 {
			return nil
		}
		key, _ := cells[0].(string)
		id, _ := cells[1].(string)
		if key == "" || id == "" {
			return nil
		}
		gaps = append(gaps, artGap{
			key:     key,
			tmdb:    id,
			season:  int(cellNumber(cells[2])),
			episode: int(cellNumber(cells[3])),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the %s gap of %s: %w", fact, library, err)
	}
	return gaps, nil
}
