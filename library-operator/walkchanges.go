package main

// walkchanges.go finds the videos that one walk Job added or replaced, for
// the webhooks the close container posts (plan 99). The walk can change a
// title's id: a folder with no provider id has a movie:path id until the
// identity phase writes the provider's id and reads the folder again. So the
// change is found on the files table, whose key is the path, and the ids are
// read only after every phase has ended.
//
// The scan container reads every video of the library before it walks and
// writes the list on the phases volume. The close container reads the list
// again after the hand-off and compares the two.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
)

// The note the scan writes on the phases volume: every video of the library
// before the walk, as one JSON object keyed by the path relative to the root.
const videosBeforeNote = "videos-before.json"

// What the comparison reads of one video: its size, and the time it was last
// written. An upgrade that keeps the file's name changes one of them.
type videoState struct {
	Size     int64 `json:"size"`
	Modified int64 `json:"modified"`
}

// The videos of one library, keyed by their path relative to the root.
type videoStates map[string]videoState

// The read of every video of one library. It needs no LIMIT, because the scan
// and the close container each hold the library's videos once, which is a
// few hundred kilobytes for a library of thousands of titles.
const videoStatesQuery = `SELECT path, size_bytes, modified FROM files WHERE library = ? AND type = ?`

// The videos the catalog holds for one library now.
func (c *Catalog) videoStates(ctx context.Context, library string) (videoStates, error) {
	states := videoStates{}
	err := c.stream(ctx, videoStatesQuery, []any{library, fileTypeVideo}, func(cells []any) error {
		if len(cells) < 3 {
			return nil
		}
		path, _ := cells[0].(string)
		states[path] = videoState{Size: int64Cell(cells[1]), Modified: int64Cell(cells[2])}
		return nil
	})
	return states, err
}

// An integer cell of a query event. The agent sends an integer as a JSON
// number, which decodes to float64.
func int64Cell(cell any) int64 {
	number, _ := cell.(float64)
	return int64(number)
}

// The two changes a walk makes to a video that a webhook reports.
const (
	videoAdded    = "added"
	videoReplaced = "replaced"
)

// The change of each video that is new beside before, or whose size or
// modified time moved, keyed by its path. A video that left the volume is in
// neither, because a webhook reports what arrived.
func changedVideos(before, after videoStates) map[string]string {
	changes := map[string]string{}
	for path, now := range after {
		earlier, held := before[path]
		switch {
		case !held:
			changes[path] = videoAdded
		case earlier != now:
			changes[path] = videoReplaced
		}
	}
	return changes
}

// The scan's half: the library's videos before the walk, on the phases
// volume. The scan writes the note only when its copy holds the Library's
// last confirmed run. A copy that has not synced reads as a library with
// fewer videos, and every video it lacks would read as added. A Library with
// no confirmed run is on its first walk, which catalogs what the volume
// already held, so the scan writes no note and the close container posts
// nothing.
func (s *scanner) noteVideosBefore(ctx context.Context) {
	if s.board == nil || !s.webhooks || s.sync.actor == "" {
		return
	}
	if err := awaitCatalogSync(ctx, s.catalog, s.sync, s.syncTimeout); err != nil {
		s.logf("could not note the videos before the walk, so this Job posts no webhook: %v", err)
		return
	}
	states, err := s.catalog.videoStates(ctx, s.library)
	if err != nil {
		s.logf("could not note the videos before the walk, so this Job posts no webhook: %v", err)
		return
	}
	data, _ := json.Marshal(states)
	if err := s.board.writeNote(videosBeforeNote, data); err != nil {
		s.logf("could not note the videos before the walk, so this Job posts no webhook: %v", err)
	}
}

// errNoVideosBefore is a Job whose scan wrote no note, which posts nothing.
var errNoVideosBefore = errors.New("the scan noted no videos before the walk")

// The close container's half: the videos the walk added or replaced, from the
// scan's note and the catalog as it is after the phases.
func (r *closeRun) walkChanges(ctx context.Context) (map[string]string, error) {
	if r.board == nil {
		return nil, errNoVideosBefore
	}
	data, err := r.board.readNote(videosBeforeNote)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errNoVideosBefore
	}
	if err != nil {
		return nil, err
	}
	var before videoStates
	if err := json.Unmarshal(data, &before); err != nil {
		return nil, fmt.Errorf("reading the note %s: %w", videosBeforeNote, err)
	}
	after, err := r.catalog.videoStates(ctx, r.library)
	if err != nil {
		return nil, fmt.Errorf("reading the videos after the walk: %w", err)
	}
	return changedVideos(before, after), nil
}

// The changed paths in order, so a body lists its files the same way on
// every run.
func changedPaths(changes map[string]string) []string {
	return slices.Sorted(maps.Keys(changes))
}
