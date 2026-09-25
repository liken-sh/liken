package main

// marksrole.go is the marks container's run: the files it asks about, the
// providers it asks, and what it leaves in the ledger and the marks table.
// The fact records every span a provider answers, as the provider answers
// it. Nothing here chooses one candidate over another.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The name of the container that runs the marks group, which is one fact.
const marksContainerName = "marks"

// The line is built once for the container, so the settings a provider states
// are read once. A container with no answerer at all is a manifest to repair,
// because the operator creates it only where a source serves the fact.
func (e *enricher) marksFact(ctx context.Context) error {
	line := newMarkLine(commaNames(os.Getenv(librarySourcesVariable)), os.Getenv, e.tallies)
	if len(line.answerers) == 0 {
		return fmt.Errorf("no provider reached this container, and the %s fact cannot ask without one", factMarks)
	}
	return e.marksGap(ctx, line)
}

// A catalog read that fails ends the container, because the gap list is the
// work. One file that fails records an error attempt, and the run carries on
// to the next. The files are asked one at a time, because the files of one
// season share one ledger file and two writers of one file would lose a
// write. Each provider's own pace sets the speed of the run either way.
func (e *enricher) marksGap(ctx context.Context, line *markLine) error {
	paths, err := e.gaps(ctx, factMarks, time.Now().UTC())
	if err != nil {
		return err
	}
	reached, found := 0, 0
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if line.exhausted() {
			e.logf("every provider of the %s fact has spent its allowance for the day, "+
				"and %d files wait for a later run", factMarks, len(paths)-reached)
			break
		}
		reached++
		if !e.inScope(path) {
			continue
		}
		file, held, err := e.catalog.markFile(ctx, e.library, path)
		if err != nil {
			return err
		}
		if !held {
			continue
		}
		if e.marksOne(ctx, line, path, file) {
			found++
		}
	}
	e.logf("found the marks of %d of the %d files the gap held", found, len(paths))
	return nil
}

// One file's ask and the record of it. A file that holds more than one
// episode is asked about no episode, because each provider places an
// episode's spans in that episode's own file, and the spans of the first
// episode would name the wrong times in a file that holds two. It records
// nothing found, so the gap does not name it again until the window passes.
func (e *enricher) marksOne(ctx context.Context, line *markLine, path string, file markFile) bool {
	folder, entry := likenFolderFor(e.kind, filepath.Join(e.root, path))
	if file.episodes > 1 {
		e.recordMarks(folder, entry, markAnswer{complete: true}, attemptNothing)
		return false
	}
	answer := line.ask(ctx, file)
	if answer.failure != nil {
		e.logf("could not read the marks of %s: %v", path, answer.failure)
	}
	e.recordMarks(folder, entry, answer, answer.result())
	return len(answer.entries) > 0
}

// The spans and the attempt are one write of one file, so a reader never sees
// an answer without its attempt. The spans of each block that answered
// replace the ones that block held for the file, because the ledger says what
// the providers hold now. A block that failed or was not asked leaves its
// spans as they are, because its silence says nothing about where the
// credits are. The spans of the folder's other files stay as they are.
func (e *enricher) recordMarks(folder, entry string, answer markAnswer, result string) {
	e.tallies.add(tallyAttempts, 1, "fact", factMarks, "result", result)
	now := time.Now().UTC()
	err := e.writer.updateLikenLedger(folder, factMarks, func(ledger *likenLedger) {
		ledger.Marks = replacedMarks(ledger.Marks, entry, answer.answered, answer.entries)
		ledger.noteAttempt(likenAttempt{Path: entry, At: now, Result: result, Provider: answer.held})
	})
	if err != nil {
		e.logf("could not record the %s attempt at %s: %v",
			factMarks, relativePath(e.root, filepath.Join(folder, entry)), err)
	}
	e.writeRows(factMarks, folder, result != attemptError)
}

// The ledger's list with one file's spans from the answered sources replaced.
// The file's other spans and the other files' spans keep their places. The
// new spans go where the first replaced span was, or after the file's last
// kept span, or at the end for a file the list did not hold.
func replacedMarks(held []markEntry, entry string, answered []string, entries []markEntry) []markEntry {
	at, last := -1, -1
	kept := make([]markEntry, 0, len(held))
	for _, one := range held {
		if one.Path == entry && slices.Contains(answered, one.Source) {
			if at < 0 {
				at = len(kept)
			}
			continue
		}
		kept = append(kept, one)
		if one.Path == entry {
			last = len(kept) - 1
		}
	}
	switch {
	case at >= 0:
	case last >= 0:
		at = last + 1
	default:
		at = len(kept)
	}
	fresh := make([]markEntry, 0, len(entries))
	for _, one := range entries {
		one.Path = entry
		fresh = append(fresh, one)
	}
	return slices.Insert(kept, at, fresh...)
}

// What one file of the gap names, read out of the local copy of the catalog.
// The file's links name the movie or the episodes it holds, and the aliases
// of the movie or of the episode's series carry the provider ids. A file the
// catalog no longer holds reads as not held, because a file may leave while a
// Job runs.
func (c *Catalog) markFile(ctx context.Context, library, path string) (markFile, bool, error) {
	file := markFile{}
	title := ""
	held := false
	err := c.stream(ctx, `SELECT files.duration_ms, IFNULL(movies.id, ''), IFNULL(episodes.series, ''), `+
		`IFNULL(episodes.season, 0), IFNULL(episodes.episode, 0) FROM files `+
		`JOIN file_items ON file_items.library = files.library AND file_items.path = files.path `+
		`LEFT JOIN movies ON movies.library = file_items.library AND movies.id = file_items.item `+
		`LEFT JOIN episodes ON episodes.library = file_items.library AND episodes.id = file_items.item `+
		`WHERE files.library = ? AND files.path = ? `+
		`AND (movies.id IS NOT NULL OR episodes.id IS NOT NULL) `+
		`ORDER BY episodes.season, episodes.episode`,
		[]any{library, path}, func(cells []any) error {
			if len(cells) < 5 {
				return nil
			}
			held = true
			file.episodes++
			if file.episodes > 1 {
				return nil
			}
			file.duration = cellNumber(cells[0])
			movie, _ := cells[1].(string)
			series, _ := cells[2].(string)
			file.movie, title = movie != "", movie+series
			file.season, file.episode = int(cellNumber(cells[3])), int(cellNumber(cells[4]))
			return nil
		})
	if err != nil || !held {
		return markFile{}, false, err
	}
	aliases, err := c.queryStrings(ctx, `SELECT alias FROM aliases WHERE library = ? AND item = ?`,
		[]any{library, title})
	if err != nil {
		return markFile{}, false, err
	}
	file.ids = aliasIDs(aliases)
	return file, true, nil
}

// The provider ids a work's aliases carry. An alias is the scope, the
// provider, and the id, as movie:tmdb:603, and the folder key is no provider
// id, so it is left out.
func aliasIDs(aliases []string) providerIDs {
	ids := providerIDs{}
	for _, alias := range aliases {
		parts := strings.SplitN(alias, ":", 3)
		if len(parts) != 3 || parts[1] == "path" {
			continue
		}
		ids[parts[1]] = parts[2]
	}
	return ids
}
