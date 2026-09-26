package main

// imdbepisodes.go fills the IMDb rating of an episode, which only the imdb
// block answers: OMDb would spend one call on each episode, and a series
// library holds tens of thousands of them. An episode's rating goes into the
// episode's own .nfo file, and its ledger is the rating.imdb file of the
// folder that holds the episode, keyed by the episode's file.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func isEpisodeID(id string) bool { return strings.HasPrefix(id, scopeEpisode+":") }

// One episode's fill. It returns the result the attempt recorded, or an
// empty result for an episode the run left: one out of scope, or one the
// reads could not answer this run. It also returns whether the .nfo file
// changed.
func (e *enricher) fillEpisodeRating(ctx context.Context, id string) (string, bool) {
	if e.datasets == nil || e.datasets.wait(ctx) != nil {
		return "", false
	}
	target := e.datasets.targets[id]
	if target == nil || target.path == "" || !e.inScope(target.path) {
		return "", false
	}
	absolute := filepath.Join(e.root, target.path)
	folder, file := filepath.Dir(absolute), filepath.Base(absolute)
	nfoPath := nfoBeside(absolute)
	document, err := os.ReadFile(nfoPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		e.logf("could not read the .nfo file of %s: %v", target.path, err)
		return e.recordEpisodeRating(folder, file, target, attemptError, ""), false
	}
	if !hasRootElement(document) {
		document = minimalNFO(nfoRootEpisode, target.title)
	}
	group := nfoGroup(factRatingIMDb)
	if fought, err := e.episodeGroupHeldByAnother(folder, file, group, document); err != nil {
		e.logf("could not read the %s of %s: %v", factRatingIMDb, target.path, err)
		return e.recordEpisodeRating(folder, file, target, attemptError, ""), false
	} else if fought {
		e.logf("another writer holds the %s of %s, so this run left it", factRatingIMDb, target.path)
		return e.recordEpisodeRating(folder, file, target, attemptFight, ""), false
	}
	rating, held := e.datasets.ratings[target.imdb]
	if target.imdb == "" || !held {
		return e.recordEpisodeRating(folder, file, target, attemptNothing, ""), false
	}
	answer := factAnswer{Rating: &rating}
	written := ratingChanged(document, answer)
	if written {
		edited, err := editElementGroup(document, group, nfoElements(factRatingIMDb, answer))
		if err == nil {
			err = e.writer.write(nfoPath, edited)
		}
		if err != nil {
			e.logf("could not write the %s of %s: %v", factRatingIMDb, target.path, err)
			return e.recordEpisodeRating(folder, file, target, attemptError, ""), false
		}
		document = edited
	}
	hash, err := groupHash(document, group)
	if err != nil {
		e.logf("could not read back the %s of %s: %v", factRatingIMDb, target.path, err)
		return e.recordEpisodeRating(folder, file, target, attemptError, ""), false
	}
	path := relativePath(e.root, absolute)
	if written {
		e.logf("wrote the %s of %s from %s", factRatingIMDb, path, providerBlockIMDb)
	} else {
		e.logf("the .nfo file of %s already holds the %s from %s", path, factRatingIMDb, providerBlockIMDb)
	}
	return e.recordEpisodeRating(folder, file, target, attemptFound, hash), written
}

// The fight check of an episode, keyed by its file, as the fight check of a
// title is keyed by the folder's own entry.
func (e *enricher) episodeGroupHeldByAnother(folder, file string, group elementGroup, document []byte) (bool, error) {
	ledger, err := readLikenLedger(folder, factRatingIMDb)
	if err != nil {
		return false, err
	}
	held, wrote := ledger.itemAt(file)
	if !wrote || held.Wrote == "" {
		return false, nil
	}
	hash, err := groupHash(document, group)
	if err != nil {
		return false, err
	}
	return hash != held.Wrote, nil
}

// The ledger entry and the attempt, in one write of one file. The entry keeps
// the episode's IMDb id, so a later run takes the id from the ledger and does
// not read title.episode again for this episode.
func (e *enricher) recordEpisodeRating(folder, file string, target *imdbTarget, result, hash string) string {
	e.tallies.add(tallyAttempts, 1, "fact", factRatingIMDb, "result", result)
	now := time.Now().UTC()
	var names providerNames
	if result == attemptFound {
		names = providerNames{providerBlockIMDb}
	}
	err := e.writer.updateLikenLedger(folder, factRatingIMDb, func(ledger *likenLedger) {
		item, _ := ledger.itemAt(file)
		item.Path = file
		if target.imdb != "" {
			item.ID = providerIDs{providerBlockIMDb: target.imdb}
		}
		if hash != "" {
			item.Provider, item.Wrote, item.Written = names, hash, now
		}
		if item.ID != nil || item.Wrote != "" {
			ledger.noteItem(item)
		}
		ledger.noteAttempt(likenAttempt{Path: file, At: now, Result: result, Provider: names,
			DatasetModified: e.datasets.modified})
	})
	if err != nil {
		e.logf("could not record the %s attempt at %s: %v", factRatingIMDb, file, err)
	}
	e.writeEpisodeRatingRows(folder, file, target.id, result == attemptFound)
	return result
}

// The episode's own rows, read back from the files the fact wrote: the body
// and the nfo_facts of the one episode, and its attempt. A write to the
// series folder's rows would read every episode of the series again for each
// episode, so this reads the one .nfo file and the one ledger.
func (e *enricher) writeEpisodeRatingRows(folder, file, id string, wrote bool) {
	if e.catalog == nil {
		return
	}
	ctx := context.Background()
	if wrote {
		if err := e.writeEpisodeBody(ctx, filepath.Join(folder, file), id); err != nil {
			e.logf("could not write the %s rows of %s: %v", factRatingIMDb, file, err)
		}
	}
	ledger, err := readLikenLedger(folder, factRatingIMDb)
	if err != nil {
		e.logf("could not read the %s ledger at %s: %v", factRatingIMDb, folder, err)
		return
	}
	var rows []attemptRow
	for _, attempt := range ledger.Attempts {
		if attempt.Path == file {
			rows = append(rows, attemptRow{Library: e.library, Item: id, Fact: factRatingIMDb,
				At: attempt.At.Unix(), Result: attempt.Result, Provider: strings.Join(attempt.Provider, ","),
				DatasetModified: unixOrZero(attempt.DatasetModified)})
		}
	}
	if _, err := e.catalog.UpsertAttempts(ctx, rows); err != nil {
		e.logf("could not write the %s attempt row of %s: %v", factRatingIMDb, file, err)
	}
}

// The body and the nfo_facts the scanner reads from the episode's .nfo file.
func (e *enricher) writeEpisodeBody(ctx context.Context, absolute, id string) error {
	data, err := os.ReadFile(nfoBeside(absolute))
	if err != nil {
		return err
	}
	metas, err := parseEpisodeNFOs(data)
	if err != nil || len(metas) == 0 {
		return err
	}
	_, err = e.catalog.UpdateItemBodies(ctx, "episodes",
		[]itemUpdate{bodyUpdate(e.library, id, metas[0].Body, metas[0].NFOFacts)})
	return err
}
