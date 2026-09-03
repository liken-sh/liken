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

// A container with no key fails before it writes anything, so the Job says
// what the pod is missing.
func (e *enricher) artFact(ctx context.Context, fact string) error {
	token := os.Getenv(tmdbTokenVariable)
	if token == "" {
		return fmt.Errorf("%s is empty, and the %s fact cannot ask a provider without it",
			tmdbTokenVariable, fact)
	}
	return e.artGap(ctx, fact, newTMDbClient(tmdbAPIBase, token))
}

// A catalog read that fails ends the container, because the gap list is the
// work. The configuration is read once, after the gap, so a library with
// every file in place makes no call at all. A provider that refuses one image
// records an error attempt, and the run carries on to the next.
func (e *enricher) artGap(ctx context.Context, fact string, client *tmdbClient) error {
	gaps, err := e.catalog.artGaps(ctx, e.library, fact, time.Now().UTC())
	if err != nil {
		return err
	}
	if len(gaps) == 0 {
		return nil
	}
	configuration, err := client.configuration(ctx)
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
		if e.artOne(ctx, client, configuration, artTypes[fact], gap) {
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
func (e *enricher) artOne(ctx context.Context, client *tmdbClient,
	configuration tmdbConfiguration, art artType, gap artGap) bool {
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

	answer, err := client.images(ctx, e.kind, art.fact, gap)
	if err != nil {
		e.logf("could not read the %s of %s: %v", art.fact, gap.key, err)
		e.recordArt(folder, art.fact, gap.entry(), "", attemptError)
		return false
	}
	image, held := chooseImage(answer.list(art.list), artLanguage)
	if !held {
		e.logf("the provider has no %s for %s", art.fact, gap.key)
		e.recordArt(folder, art.fact, gap.entry(), "", attemptNothing)
		return false
	}
	return e.writeArt(ctx, client, configuration, art, gap, folder, target, image)
}

// The download and the write. The bytes live from the answer to the rename
// and no longer. A create that finds the file there answers as the read above
// does, because another writer reached it first.
func (e *enricher) writeArt(ctx context.Context, client *tmdbClient, configuration tmdbConfiguration,
	art artType, gap artGap, folder, target string, image tmdbImage) bool {
	address := configuration.imageURL(configuration.sizeFor(art), image.FilePath)
	data, err := client.fetchFile(ctx, address)
	if err != nil {
		e.logf("could not read %s: %v", address, err)
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
	e.logf("wrote the %s of %s from tmdb %s", art.fact, gap.key, gap.tmdb)
	e.recordArt(folder, art.fact, gap.entry(), providerTMDb, attemptFound)
	return true
}

// The name the ledger records for the provider that answered.
const providerTMDb = "tmdb"

// The item entry and the attempt are one write of one file, so a reader never
// sees an answer without its attempt. A provider name of nothing is a miss or
// an error, which the attempt itself states.
func (e *enricher) recordArt(folder, fact, entry, provider, result string) {
	now := time.Now().UTC()
	err := e.writer.updateLikenLedger(folder, fact, func(ledger *likenLedger) {
		if provider != "" {
			item := likenItem{Path: entry, Provider: provider}
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
}

// One art fact's work list, out of the local copy of the catalog, with the
// same query the reporter counts the gap with. Every row names the file to
// write, the TMDb id to ask for, and the season and episode where the fact
// needs them.
func (c *Catalog) artGaps(ctx context.Context, library, fact string, now time.Time) ([]artGap, error) {
	cutoff := now.Add(-defaultRetryInterval).Unix()
	var gaps []artGap
	err := c.stream(ctx, gapQueries[fact], []any{library, cutoff}, func(cells []any) error {
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
