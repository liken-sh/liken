package main

// marksrole.go is the marks container's run: the files it asks about, the
// providers it asks, and what it leaves in the ledger and the marks table.
// The fact records every span a provider answers, as the provider answers
// it. Nothing here chooses one candidate over another.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The name of the container that runs the marks group, which is one fact.
const marksContainerName = "marks"

// The answerers the marks container asks, in the order the Library's sources
// name their blocks.
type markLine struct {
	answerers []markAnswerer
}

// The answerer of each block the marks fact can ask. TheIntroDB takes the
// token where one reached the container, and IntroDB takes none.
var markAnswerers = map[string]func(base, token string, record *tallies) markAnswerer{
	providerBlockTheIntroDB: func(base, token string, record *tallies) markAnswerer {
		client := newTheIntroDBClient(base, token)
		client.recordTo(record)
		return newTheIntroDBMarkAnswerer(client)
	},
	providerBlockIntroDB: func(base, _ string, record *tallies) markAnswerer {
		client := newIntroDBClient(base)
		client.recordTo(record)
		return newIntroDBMarkAnswerer(client)
	},
}

// The line, in the order the Library's own spec.sources names the blocks.
func newMarkLine(blocks []string, value func(string) string, record *tallies) *markLine {
	return &markLine{answerers: recordingAnswerers(blocks, value, record, markAnswerers)}
}

// One file's ask: every answerer, because the marks of a file are the union
// of what the providers hold. A provider that is down does not discard the
// answers of the other blocks, so the ask returns an error only when no block
// answered at all.
//
// A provider that answers 429 after its cooldowns has spent its allowance for
// the day, and it leaves the line for the rest of this run, so the run does
// not spend three requests a file on an answer that cannot change until the
// allowance resets.
func (l *markLine) ask(ctx context.Context, file markFile) ([]markEntry, []string, error) {
	var entries []markEntry
	var blocks []string
	var failure error
	remaining := make([]markAnswerer, 0, len(l.answerers))
	for _, one := range l.answerers {
		held, err := one.marks(ctx, file)
		if !answeredWith(err, http.StatusTooManyRequests) {
			remaining = append(remaining, one)
		}
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
	l.answerers = remaining
	if len(entries) == 0 {
		return nil, nil, failure
	}
	return entries, blocks, nil
}

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
		if len(line.answerers) == 0 {
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
		e.recordMarks(folder, entry, []markEntry{}, nil, attemptNothing)
		return false
	}
	entries, blocks, err := line.ask(ctx, file)
	if err != nil {
		e.logf("could not read the marks of %s: %v", path, err)
		e.recordMarks(folder, entry, nil, nil, attemptError)
		return false
	}
	if len(entries) == 0 {
		e.recordMarks(folder, entry, []markEntry{}, nil, attemptNothing)
		return false
	}
	e.recordMarks(folder, entry, entries, blocks, attemptFound)
	return true
}

// The spans and the attempt are one write of one file, so a reader never sees
// an answer without its attempt. The spans of this file replace the ones the
// ledger held for it, because the ledger says what the providers hold now.
// A provider that was down leaves the spans as they are, because a failed ask
// says nothing about where the credits are. The spans of the folder's other
// files stay as they are.
func (e *enricher) recordMarks(folder, entry string, entries []markEntry, blocks []string, result string) {
	e.tallies.add(tallyAttempts, 1, "fact", factMarks, "result", result)
	now := time.Now().UTC()
	err := e.writer.updateLikenLedger(folder, factMarks, func(ledger *likenLedger) {
		if result != attemptError {
			ledger.Marks = replacedMarks(ledger.Marks, entry, entries)
		}
		ledger.noteAttempt(likenAttempt{Path: entry, At: now, Result: result, Provider: blocks})
	})
	if err != nil {
		e.logf("could not record the %s attempt at %s: %v",
			factMarks, relativePath(e.root, filepath.Join(folder, entry)), err)
	}
	e.writeRows(factMarks, folder, result != attemptError)
}

// The ledger's list with one file's spans replaced. The other files' spans
// keep their places, and the new spans go where the file's first span was, or
// at the end for a file the list did not hold.
func replacedMarks(held []markEntry, entry string, entries []markEntry) []markEntry {
	at := slices.IndexFunc(held, func(one markEntry) bool { return one.Path == entry })
	kept := slices.DeleteFunc(slices.Clone(held), func(one markEntry) bool { return one.Path == entry })
	if at < 0 || at > len(kept) {
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
