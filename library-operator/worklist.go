package main

// worklist.go is the file that carries one heavy fact's gap from the library
// Job to the fact's worker Job. The library Job holds the catalog, so it reads
// the gap. The worker Job holds no catalog, so it reads this file. The close
// container of every library Job writes the file after its phases end, with
// the same gap query the reporter counts the gap with.
//
// The file is JSON lines, one video per line, sorted by path, so the worker
// reads one line at a time and finishes the videos of one title folder
// together.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The directory under the root's .liken directory that holds the work lists:
// one directory per Library, by its namespace and name, and one file per fact
// in it. The walk skips every name that starts with a dot, so it never reads
// a list as media, and mark and sweep acts on catalog rows and never on
// files, so it never removes one. No other tool reads the root's .liken
// directory.
//
// Two clusters can mount one volume, each with a Library over the same root.
// The facts beside the media are the same facts for both, but a list is one
// Library's gap, read from its own catalog with its own refresh times, so
// each Library writes and reads its own.
const workListDirectory = "worklists"

// The file one Library's list of one fact is in. library is the Library's
// key, its namespace and its name.
func workListPath(root, library, fact string) string {
	return filepath.Join(root, likenDirectory, workListDirectory, filepath.FromSlash(library), fact+".jsonl")
}

// One video of a work list. The size is the size the walk read, which is the
// size the probe recorded wherever the length comes from a probe record,
// because the walk takes no length from a record of another size. The worker
// compares it with the file on the volume before it opens the file. Listed is
// when the library Job wrote the list, so the worker can tell an attempt made
// after the list from one the list already took into account.
type workItem struct {
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	DurationMs int64     `json:"durationMs,omitempty"`
	Listed     time.Time `json:"listed"`
}

// The length of the video, which says how many thumbnails cover it.
func (i workItem) duration() time.Duration {
	return time.Duration(i.DurationMs) * time.Millisecond
}

// One fact's gap out of the local copy of the catalog, as a work list. A
// worker fact's gap query selects the path, the size, and the length of each
// video, in that order.
func (c *Catalog) workItems(ctx context.Context, fact, library string, now, refresh time.Time) ([]workItem, error) {
	var items []workItem
	err := c.stream(ctx, gapQueries[fact], gapParams(fact, library, now, refresh), func(cells []any) error {
		if len(cells) < 3 {
			return nil
		}
		path, _ := cells[0].(string)
		if path == "" {
			return nil
		}
		items = append(items, workItem{
			Path:       path,
			Size:       cellNumber(cells[1]),
			DurationMs: cellNumber(cells[2]),
			Listed:     now,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the %s gap of %s: %w", fact, library, err)
	}
	slices.SortFunc(items, func(a, b workItem) int { return strings.Compare(a.Path, b.Path) })
	return items, nil
}

// The whole list goes onto the volume in one write: a temporary and a rename,
// so a worker that reads the list while the next one lands reads the whole of
// one list or the whole of the other.
//
// Every library Job writes the list, and each list carries its own time, so
// a list of the same videos would differ from the one before it in the time
// alone. The list on the volume stays where it names the same videos, with
// the same sizes and lengths, in the same order. Jellyfin's library monitor
// reads a rename under the library's root as a change to the whole library,
// because the root is the nearest item it holds above .liken, and a rewrite
// on every Job starts a refresh of the whole library each time.
//
// The earlier time still serves the worker. The catalog takes a video out of
// the gap for the window of an attempt it has read, so a list of the same
// videos means the catalog read no attempt on them since the earlier list.
// An attempt it has not read yet, made after the earlier list, makes the
// worker pass over the video, which is the rule the time exists for.
func (w *volumeWriter) writeWorkList(root, library, fact string, items []workItem) error {
	if held, there, err := heldWorkList(root, library, fact); err == nil && there &&
		slices.EqualFunc(held, items, workItem.sameVideo) {
		return nil
	}
	var lines []byte
	for _, item := range items {
		line, err := json.Marshal(item)
		if err != nil {
			return err
		}
		lines = append(append(lines, line...), '\n')
	}
	path := workListPath(root, library, fact)
	return w.writeInto(filepath.Dir(path), filepath.Base(path), lines)
}

// Whether two items name the same file of the same length, whatever the time
// of the list each one came from.
func (i workItem) sameVideo(other workItem) bool {
	return i.Path == other.Path && i.Size == other.Size && i.DurationMs == other.DurationMs
}

// The list on the volume, and whether there is one. An empty list that is
// there is a list, so a Job that finds no gap still leaves one.
func heldWorkList(root, library, fact string) ([]workItem, bool, error) {
	if _, err := os.Stat(workListPath(root, library, fact)); err != nil {
		return nil, false, err
	}
	items, err := readWorkList(root, library, fact)
	return items, err == nil, err
}

// One fact's list as the last library Job wrote it. A root with no list reads
// as no work and not as an error, because a Library that has turned a fact on
// has no list until its next library Job ends.
func readWorkList(root, library, fact string) ([]workItem, error) {
	file, err := os.Open(workListPath(root, library, fact))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var items []workItem
	lines := bufio.NewScanner(file)
	// A path is at most a few hundred bytes, so a line never reaches the
	// scanner's limit, and a line that does is a list to repair.
	for lines.Scan() {
		var item workItem
		if err := json.Unmarshal(lines.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("reading the %s work list: %w", fact, err)
		}
		items = append(items, item)
	}
	if err := lines.Err(); err != nil {
		return nil, fmt.Errorf("reading the %s work list: %w", fact, err)
	}
	return items, nil
}
