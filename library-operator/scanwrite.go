package main

// scanwrite.go writes a walk's rows to the catalog. The scanner is the one
// writer of its agent's catalog. It upserts every row a walk read, and a
// separate mark-and-sweep pass in prune.go removes the rows a walk did not
// reach.

import "context"

// scanFlushBatch bounds how many items the streaming full walk buffers before it
// writes them, so the walk holds one batch and never the whole library. It is a
// var so a test drives several flushes over a small set, the way pruneBatch
// bounds the prune.
var scanFlushBatch = 512

// flushWalk writes a buffer of walked rows to the catalog and marks their keys
// with the walk's epoch. Both the streaming full walk and a webhook rescan write
// a folder through it.
func flushWalk(ctx context.Context, catalog *Catalog, result *walkResult, epoch int64) error {
	if err := upsertWalk(ctx, catalog, result); err != nil {
		return err
	}
	_, err := catalog.markSeen(ctx, markKeys(result), epoch)
	return err
}

// upsertWalk writes every row a walk produced: the items, the files and their
// item links, the aliases, and the attempts read out of the .liken files.
func upsertWalk(ctx context.Context, catalog *Catalog, result *walkResult) error {
	steps := []func() (int, error){
		func() (int, error) { return catalog.UpsertMovies(ctx, result.movies) },
		func() (int, error) { return catalog.UpsertSets(ctx, result.sets) },
		func() (int, error) { return catalog.UpsertSeries(ctx, result.series) },
		func() (int, error) { return catalog.UpsertEpisodes(ctx, result.episodes) },
		func() (int, error) { return catalog.UpsertFiles(ctx, result.files) },
		func() (int, error) { return catalog.UpsertFileItems(ctx, result.files) },
		func() (int, error) { return catalog.UpsertAliases(ctx, result.aliases) },
		func() (int, error) { return catalog.UpsertAttempts(ctx, result.attempts) },
	}
	for _, step := range steps {
		if _, err := step(); err != nil {
			return err
		}
	}
	return nil
}
