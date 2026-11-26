package main

// walkchanges.go finds the videos that one walk Job added or replaced, and
// the videos it took away, for the webhooks the close container posts (plans
// 99 and 100). The walk can change a title's id: a folder with no provider id
// has a movie:path id until the identity phase writes the provider's id and
// reads the folder again. So the change is found on the files table, whose
// key is the path, and the ids are read only after every phase has ended.
//
// The scan container reads every video of the library before it walks, with
// the items each one holds, and writes the list on the phases volume. The
// close container reads the list again after the hand-off and compares the
// two.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"slices"
	"strings"
)

// The notes the scan writes on the phases volume. The first holds every video
// of the library before the walk, as one JSON object keyed by the path
// relative to the root. The second holds the whole rows of the videos under
// the folders a folders walk names, so a receiver can compare a video that
// left with the one that arrived. Whole rows of every video would be tens of
// megabytes in a large library, so a full walk writes no second note.
const (
	videosBeforeNote = "videos-before.json"
	rowsBeforeNote   = "rows-before.json"
)

// What the comparison reads of one video: its size, the time it was last
// written, and the items it holds. An upgrade that keeps the file's name
// changes the size or the time, and the items say which movie or episode lost
// the video when it leaves.
type videoState struct {
	Size     int64    `json:"size"`
	Modified int64    `json:"modified"`
	Items    []string `json:"items,omitempty"`
}

// The videos of one library, keyed by their path relative to the root.
type videoStates map[string]videoState

// The read of every video of one library, one row for each item it holds. It
// needs no LIMIT, because the scan and the close container each hold the
// library's videos once, which is under a megabyte for a library of thousands
// of titles.
const videoStatesQuery = `SELECT f.path, f.size_bytes, f.modified, i.item FROM files f ` +
	`LEFT JOIN file_items i ON i.library = f.library AND i.path = f.path ` +
	`WHERE f.library = ? AND f.type = ? ORDER BY f.path, i.item`

// The videos the catalog holds for one library now.
func (c *Catalog) videoStates(ctx context.Context, library string) (videoStates, error) {
	states := videoStates{}
	err := c.stream(ctx, videoStatesQuery, []any{library, fileTypeVideo}, func(cells []any) error {
		if len(cells) < 4 {
			return nil
		}
		path, _ := cells[0].(string)
		state, held := states[path]
		if !held {
			state = videoState{Size: int64Cell(cells[1]), Modified: int64Cell(cells[2])}
		}
		if item, _ := cells[3].(string); item != "" {
			state.Items = append(state.Items, item)
		}
		states[path] = state
		return nil
	})
	return states, err
}

// The whole rows of the videos under the folders a folders walk names, in the
// form a body carries them, keyed by their path. Each value stays encoded
// until a body names the video in a previous list, so the close container
// decodes only the videos the walk took away.
type videoRows map[string]json.RawMessage

// How many bytes of rows the scan writes before the walk. A long series
// folder holds thousands of videos, and the webhooks can name dozens of
// folders, so whole rows of all of them would not fit in the memory of the
// scan container or the close container. A video the budget leaves out
// carries the columns of the first note, as a video of a full walk does. It
// is a var so a test drives a small budget.
var rowsBeforeBudget = 4 << 20

// How many videos the scan reads the rows of in one batch of queries. A
// series folder can hold thousands of videos, and a query for each video
// would make thousands of requests to the agent before the walk starts.
const rowsBeforeBatch = 100

// Writes the whole rows of each video under the folders, a batch of videos at
// a time, until the next video would pass the budget. So the scan holds one
// batch's rows at once, and the note is never larger than the budget.
func (c *Catalog) writeVideoRows(ctx context.Context, w io.Writer, library string, folders []string) error {
	budget := rowsBeforeBudget - len("{}")
	separator := ""
	if _, err := io.WriteString(w, "{"); err != nil {
		return err
	}
	for _, folder := range folders {
		paths, err := c.queryStrings(ctx, `SELECT path FROM files WHERE library = ? AND `+
			pathScopeClause("path")+` AND type = ? ORDER BY path`,
			append(append([]any{library}, pathScopeParams(folder)...), fileTypeVideo))
		if err != nil {
			return err
		}
		for batch := range slices.Chunk(paths, rowsBeforeBatch) {
			rows, err := c.videoRowsOf(ctx, library, batch)
			if err != nil {
				return err
			}
			for _, path := range batch {
				file, held := rows[path]
				if !held {
					continue
				}
				key, _ := json.Marshal(path)
				value, _ := json.Marshal(file)
				entry := separator + string(key) + ":" + string(value)
				if len(entry) > budget {
					_, err := io.WriteString(w, "}")
					return err
				}
				if _, err := io.WriteString(w, entry); err != nil {
					return err
				}
				budget -= len(entry)
				separator = ","
			}
		}
	}
	_, err := io.WriteString(w, "}")
	return err
}

// The files rows of some videos, each with its streams and marks rows, keyed
// by path: one query for each table.
func (c *Catalog) videoRowsOf(ctx context.Context, library string, paths []string) (map[string]walkFile, error) {
	in := `(` + strings.TrimSuffix(strings.Repeat("?,", len(paths)), ",") + `)`
	params := []any{library}
	for _, path := range paths {
		params = append(params, path)
	}
	files, err := c.bodyRows(ctx, `SELECT * FROM files WHERE library = ? AND path IN `+in, params...)
	if err != nil {
		return nil, err
	}
	rows := make(map[string]walkFile, len(files))
	for _, row := range files {
		path, _ := row["path"].(string)
		rows[path] = walkFile{Row: row, Streams: []map[string]any{}, Marks: []map[string]any{}}
	}
	for _, table := range []string{"streams", "marks"} {
		children, err := c.bodyRows(ctx, `SELECT * FROM `+table+` WHERE library = ? AND path IN `+in+
			` ORDER BY path, ordinal`, params...)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			path, _ := child["path"].(string)
			file, held := rows[path]
			if !held {
				continue
			}
			if table == "streams" {
				file.Streams = append(file.Streams, child)
			} else {
				file.Marks = append(file.Marks, child)
			}
			rows[path] = file
		}
	}
	return rows, nil
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
// neither, because a file of a body is a video that arrived.
func changedVideos(before, after videoStates) map[string]string {
	changes := map[string]string{}
	for path, now := range after {
		earlier, held := before[path]
		switch {
		case !held:
			changes[path] = videoAdded
		case earlier.Size != now.Size || earlier.Modified != now.Modified:
			changes[path] = videoReplaced
		}
	}
	return changes
}

// What one walk changed: the videos before and after it, the change of each
// video that arrived, the rows the scan kept of the videos before, and the
// paths of each item's videos before and after, so the work for one item is
// as large as that item's own videos.
type walkChange struct {
	before videoStates
	after  videoStates
	files  map[string]string
	rows   videoRows
	// The scope of the walk the scan ran, full or folders, and empty where
	// the scan noted none.
	scope       string
	beforeItems map[string][]string
	afterItems  map[string][]string
}

func newWalkChange(before, after videoStates, rows videoRows) walkChange {
	return walkChange{before: before, after: after, files: changedVideos(before, after), rows: rows,
		beforeItems: pathsOfItems(before), afterItems: pathsOfItems(after)}
}

// The paths of each item's videos.
func pathsOfItems(states videoStates) map[string][]string {
	paths := map[string][]string{}
	for path, state := range states {
		for _, item := range state.Items {
			paths[item] = append(paths[item], path)
		}
	}
	return paths
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
	// The rows go first, because the close container reads the note of the
	// videos as the sign that the scan noted the walk. A full walk keeps no
	// rows, so it writes no note of them.
	if folders := s.noteFolders(); len(folders) > 0 {
		if err := s.board.writeNoteWith(rowsBeforeNote, func(w io.Writer) error {
			return s.catalog.writeVideoRows(ctx, w, s.library, folders)
		}); err != nil {
			s.logf("could not note the videos before the walk, so this Job posts no webhook: %v", err)
			return
		}
	}
	data, _ := json.Marshal(states)
	if err := s.board.writeNote(videosBeforeNote, data); err != nil {
		s.logf("could not note the videos before the walk, so this Job posts no webhook: %v", err)
		return
	}
	s.noted = true
}

// The note of the walk's scope, which the scan writes after the walk. A
// folders walk falls back to the whole root when a folder the webhooks named
// does not map onto the volume, and the body names that walk full.
const walkScopeNote = "walk-scope"

// The scan's note of the walk it ran, beside a note of the videos before it.
func (s *scanner) noteWalkScope() {
	if !s.noted {
		return
	}
	scope := walkFolders
	if s.walkedWhole || len(s.scanPaths) == 0 {
		scope = walkFull
	}
	if err := s.board.writeNote(walkScopeNote, []byte(scope)); err != nil {
		s.logf("could not note the scope of the walk: %v", err)
	}
}

// The title folders a folders walk names, relative to the root, whose videos
// the scan keeps whole rows of. A path the scan cannot map onto a title
// folder keeps none, and a full walk names no folder.
func (s *scanner) noteFolders() []string {
	var folders []string
	for _, scanPath := range s.scanPaths {
		absolute := s.resolveWebhookPath(scanPath)
		if absolute == "" {
			continue
		}
		if folder := s.titleFolderOf(absolute); folder != "" {
			folders = append(folders, relativePath(s.root, folder))
		}
	}
	return folders
}

// errNoVideosBefore is a Job whose scan wrote no note, which posts nothing.
var errNoVideosBefore = errors.New("the scan noted no videos before the walk")

// The close container's half: what the walk changed, from the scan's notes
// and the catalog as it is after the phases.
func (r *closeRun) walkChanges(ctx context.Context) (walkChange, error) {
	if r.board == nil {
		return walkChange{}, errNoVideosBefore
	}
	var before videoStates
	if err := r.readNote(videosBeforeNote, &before); err != nil {
		return walkChange{}, err
	}
	var rows videoRows
	if err := r.readNote(rowsBeforeNote, &rows); err != nil && !errors.Is(err, errNoVideosBefore) {
		return walkChange{}, err
	}
	after, err := r.catalog.videoStates(ctx, r.library)
	if err != nil {
		return walkChange{}, fmt.Errorf("reading the videos after the walk: %w", err)
	}
	change := newWalkChange(before, after, rows)
	if scope, err := r.board.readNote(walkScopeNote); err == nil {
		change.scope = string(scope)
	}
	return change, nil
}

// One note of the scan, decoded into value. A note the scan did not write is
// errNoVideosBefore.
func (r *closeRun) readNote(name string, value any) error {
	data, err := r.board.readNote(name)
	if errors.Is(err, fs.ErrNotExist) {
		return errNoVideosBefore
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("reading the note %s: %w", name, err)
	}
	return nil
}

// The changed paths in order, so a body lists its files the same way on
// every run.
func changedPaths(changes map[string]string) []string {
	return slices.Sorted(maps.Keys(changes))
}
