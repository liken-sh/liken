package main

// orphans.go finds the outputs of a video that is gone and the temporaries
// that stopped writers left beside the media (plan 101). The operator is
// responsible for the outputs it derives from one video: the tile directory
// beside the video, and the appearances and titles records under .liken.
// Each one is a cache that a decode makes again, and none is of use once its
// video is gone, so the rules are three checks and no record of who wrote the
// file. The scan mounts the volume read-only, so a full walk names what it
// found on the phases volume, and the close container removes each one
// through the doors in volumewrite.go, which check again.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The two kinds of output the operator removes for a gone video.
const (
	orphanTiles   = "tiles"
	orphanRecords = "records"
)

// One output of a gone video, as the walk names it: the title folder relative
// to the root, and the entry under it. A tile directory's entry is the
// directory, and a record's entry is the video the records are named for.
type orphanOutput struct {
	Title string `json:"title"`
	Entry string `json:"entry"`
	Kind  string `json:"kind"`
}

// The extensions of a file that counts as a video when the sweep asks
// whether a video is there: Jellyfin's own list (NamingOptions in Jellyfin
// 12.4), which is wider than the set the walk catalogs. A wider set keeps
// more tiles, never fewer, so a format the walk does not read still keeps
// its tiles. Any file of the same name is not counted, because the .nfo file
// a download manager leaves would keep every output.
var presentVideoExtensions = map[string]bool{
	".001": true, ".3g2": true, ".3gp": true, ".amv": true, ".asf": true, ".asx": true,
	".avi": true, ".bin": true, ".bivx": true, ".divx": true, ".dv": true, ".dvr-ms": true,
	".f4v": true, ".fli": true, ".flv": true, ".ifo": true, ".img": true, ".iso": true,
	".m2t": true, ".m2ts": true, ".m2v": true, ".m4v": true, ".mkv": true, ".mk3d": true,
	".mov": true, ".mp4": true, ".mpe": true, ".mpeg": true, ".mpg": true, ".mts": true,
	".mxf": true, ".nrg": true, ".nsv": true, ".nuv": true, ".ogm": true, ".ogv": true,
	".pva": true, ".qt": true, ".rec": true, ".rm": true, ".rmvb": true, ".strm": true,
	".svq3": true, ".tp": true, ".ts": true, ".ty": true, ".viv": true, ".vob": true,
	".vp3": true, ".webm": true, ".wmv": true, ".wtv": true, ".xvid": true,
}

func isPresentVideo(name string) bool {
	return presentVideoExtensions[strings.ToLower(filepath.Ext(name))] || videoExtensions[strings.ToLower(filepath.Ext(name))]
}

// The suffixes a download manager gives a video while it moves the file. A
// name with one of them is the video, still there.
var videoMoveSuffixes = []string{".backup~", ".partial~"}

// The record names in a folder's .liken directory that are named for one
// video, by the directory that holds them.
var videoRecordSuffixes = map[string][]string{
	appearancesRecordsDirectory: {".jsonl", ".matches.json", ".spans.json"},
	titlesRecordsDirectory:      {".jsonl"},
}

// How long a temporary beside the media must go unchanged before the sweep
// removes it: twice the deadline of a worker Job, so no worker that
// Kubernetes still lets run holds it.
const staleTemporaryAge = 2 * factWorkerDeadline

// The note the scan writes for the close container.
const orphansNote = "orphaned-outputs.json"

type orphansNoteBody struct {
	Outputs     []orphanOutput `json:"outputs,omitempty"`
	Temporaries []string       `json:"temporaries,omitempty"`
}

// Whether a video is in its directory: the name itself, or the name with a
// suffix a move gives it. A stat that fails another way reads as the video
// being there, so a share that refuses one stat removes nothing.
func videoPresent(dir, name string) bool {
	for _, candidate := range append([]string{name}, suffixed(name)...) {
		if _, err := os.Lstat(filepath.Join(dir, candidate)); !errors.Is(err, fs.ErrNotExist) {
			return true
		}
	}
	return false
}

func suffixed(name string) []string {
	names := make([]string, 0, len(videoMoveSuffixes))
	for _, suffix := range videoMoveSuffixes {
		names = append(names, name+suffix)
	}
	return names
}

// The video a directory entry stands for, and false for an entry that is no
// video. A name a move gave the video stands for the video.
func videoName(name string) (string, bool) {
	for _, suffix := range videoMoveSuffixes {
		name = strings.TrimSuffix(name, suffix)
	}
	if skipName(name) || !isPresentVideo(name) {
		return "", false
	}
	return name, true
}

// The base names, lowercased, of the videos among a directory's entries. A
// tile directory is named for a video without its extension, so this is
// the set a tile directory's name is checked against, for any extension and
// in any case.
func videoBases(entries []os.DirEntry) map[string]bool {
	bases := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if video, ok := videoName(entry.Name()); ok {
			bases[strings.ToLower(strings.TrimSuffix(video, filepath.Ext(video)))] = true
		}
	}
	return bases
}

// Whether no video of a directory derives this tile directory's name.
func tilesOrphaned(bases map[string]bool, tiles string) bool {
	return !bases[strings.ToLower(strings.TrimSuffix(tiles, filepath.Ext(tiles)))]
}

// The directories of one title: the folder, and the extras folders of a film
// or every folder of a series. A Library's ignore set keeps its folders out,
// as the walk keeps them out.
func titleDirectories(kind, title string, entries []os.DirEntry, ignore ignoreSet) []string {
	dirs := []string{title}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 || skipName(name) || ignore.skips(name) {
			continue
		}
		if strings.EqualFold(filepath.Ext(name), trickplayExtension) {
			continue
		}
		if kind == libraryKindSeries || extrasFolderName(name) != "" {
			dirs = append(dirs, filepath.Join(title, name))
		}
	}
	return dirs
}

// Whether a title has a video in any of its directories. A title with none
// is a folder that is still arriving, or a share that hides its videos, so
// nothing of it is removed. A directory that will not list reads as holding
// a video.
func titleHoldsVideo(kind, title string) bool {
	entries, err := os.ReadDir(title)
	if err != nil {
		return true
	}
	for _, dir := range titleDirectories(kind, title, entries, ignoreSet{}) {
		held := entries
		if dir != title {
			if held, err = os.ReadDir(dir); err != nil {
				return true
			}
		}
		if len(videoBases(held)) > 0 {
			return true
		}
	}
	return false
}

// What a walk of one title found: the outputs of its gone videos and its
// stale temporaries, the temporaries as absolute paths.
type titleSweep struct {
	outputs     []orphanOutput
	temporaries []string
}

// The sum of two sweeps, for a result that holds several titles.
func (t titleSweep) add(other titleSweep) titleSweep {
	t.outputs = append(t.outputs, other.outputs...)
	t.temporaries = append(t.temporaries, other.temporaries...)
	return t
}

// sweepTitle reads one title's directories and names what the close
// container removes. A directory that will not list names nothing.
func (scan folderScan) sweepTitle(title string, now time.Time) titleSweep {
	var swept titleSweep
	entries, err := os.ReadDir(title)
	if err != nil {
		return swept
	}
	relativeTitle := relativePath(scan.root, title)
	holdsVideo := false
	type listed struct {
		dir     string
		entries []os.DirEntry
	}
	var dirs []listed
	for _, dir := range titleDirectories(scan.kind, title, entries, scan.ignore) {
		held := entries
		if dir != title {
			if held, err = os.ReadDir(dir); err != nil {
				continue
			}
		}
		dirs = append(dirs, listed{dir: dir, entries: held})
		holdsVideo = holdsVideo || len(videoBases(held)) > 0
	}
	for _, d := range dirs {
		for _, entry := range d.entries {
			if strings.Contains(entry.Name(), likenTempMark) && temporaryStale(filepath.Join(d.dir, entry.Name()), now) {
				swept.temporaries = append(swept.temporaries, filepath.Join(d.dir, entry.Name()))
			}
		}
		if !holdsVideo {
			continue
		}
		bases := videoBases(d.entries)
		for _, entry := range d.entries {
			name := entry.Name()
			if entry.IsDir() && entry.Type()&fs.ModeSymlink == 0 && !skipName(name) &&
				strings.EqualFold(filepath.Ext(name), trickplayExtension) && tilesOrphaned(bases, name) {
				swept.outputs = append(swept.outputs, orphanOutput{Title: relativeTitle,
					Entry: relativePath(title, filepath.Join(d.dir, name)), Kind: orphanTiles})
			}
		}
		if !holdsLiken(d.entries) {
			continue
		}
		for _, video := range recordedVideos(d.dir) {
			if !videoPresent(d.dir, video) {
				swept.outputs = append(swept.outputs, orphanOutput{Title: relativeTitle,
					Entry: relativePath(title, filepath.Join(d.dir, video)), Kind: orphanRecords})
			}
		}
	}
	return swept
}

// Whether a directory's entries include its .liken directory, so a folder
// with none costs no read of its records.
func holdsLiken(entries []os.DirEntry) bool {
	for _, entry := range entries {
		if entry.Name() == likenDirectory {
			return true
		}
	}
	return false
}

// The videos a directory's records are named for, each once, in the order the
// listings give them.
func recordedVideos(dir string) []string {
	var videos []string
	seen := map[string]bool{}
	for _, records := range []string{appearancesRecordsDirectory, titlesRecordsDirectory} {
		entries, err := os.ReadDir(filepath.Join(dir, likenDirectory, records))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			video, ok := recordVideo(records, entry.Name())
			if ok && entry.Type().IsRegular() && !seen[video] {
				seen[video] = true
				videos = append(videos, video)
			}
		}
	}
	return videos
}

// The video a record's name is for, and false for a name that is no record
// of one video, such as the gallery or a partial file.
func recordVideo(records, name string) (string, bool) {
	if isPartialName(name) {
		return "", false
	}
	for _, suffix := range videoRecordSuffixes[records] {
		video, cut := strings.CutSuffix(name, suffix)
		if cut && isPresentVideo(video) {
			return video, true
		}
	}
	return "", false
}

// Whether a temporary beside the media has gone unchanged for
// staleTemporaryAge. The age counts from the newest modified time or change
// time of the entry and everything under it, because ffmpeg writes each sheet
// into the layout folder as it decodes, and a rename sets the change time.
// An entry that cannot be read is not stale.
func temporaryStale(path string, now time.Time) bool {
	newest, err := newestChange(path)
	return err == nil && now.Sub(newest) > staleTemporaryAge
}

func newestChange(path string) (time.Time, error) {
	var newest time.Time
	err := filepath.WalkDir(path, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		var stat syscall.Stat_t
		if err := syscall.Lstat(at, &stat); err != nil {
			return &os.PathError{Op: "lstat", Path: at, Err: err}
		}
		for _, at := range []time.Time{
			time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec), time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec),
		} {
			if at.After(newest) {
				newest = at
			}
		}
		return nil
	})
	return newest, err
}

// The aside tree of replaceEarlierTree, and the tile directory it was moved
// from. A worker that stopped between its two renames leaves the aside tree
// as the only copy of the tiles, so it stays while that directory is missing
// and its video is there.
func asideTreeTiles(path string) (string, bool) {
	name := filepath.Base(path)
	tiles, _, found := strings.Cut(name, ".earlier"+likenTempMark)
	if !found {
		return "", false
	}
	return filepath.Join(filepath.Dir(path), strings.TrimPrefix(tiles, ".")), true
}

// Checks an entry a note or a caller names: relative, with no climb, and at
// most a folder of the title and a name.
func titleEntry(root, title, entry string) (string, error) {
	if !filepath.IsLocal(title) || !filepath.IsLocal(entry) || len(splitPath(entry)) > 2 {
		return "", fmt.Errorf("refusing %q under %q: it is not a name in the title or in one folder of it", entry, title)
	}
	return filepath.Join(root, title, entry), nil
}

// The scan's half: what the walk found, written for the close container. The
// scan writes it only for a walk that counts, so a webhook rescan, a failed
// walk, and a copy that never synced remove nothing.
func (s *scanner) noteOrphans() {
	if s.board == nil || (len(s.orphans) == 0 && len(s.staleTemporaries) == 0) {
		return
	}
	note := orphansNoteBody{Outputs: s.orphans}
	for _, path := range s.staleTemporaries {
		note.Temporaries = append(note.Temporaries, relativePath(s.root, path))
	}
	data, _ := json.Marshal(note)
	if err := s.board.writeNote(orphansNote, data); err != nil {
		s.logf("could not hand the outputs of gone videos to the close container: %v", err)
	}
}

// Whether the walk this scan runs may name outputs to remove: the scan has a
// write its copy must hold, and the copy reached it. A first walk has none. A
// webhook rescan names nothing, so it waits for nothing.
func (s *scanner) awaitSweepSync(ctx context.Context) bool {
	if s.board == nil || s.sync.actor == "" || len(s.scanPaths) > 0 {
		return false
	}
	if err := awaitCatalogSync(ctx, s.catalog, s.sync, s.syncTimeout); err != nil {
		s.logf("removes no output of a gone video: %v", err)
		return false
	}
	return true
}

// The close container's half: each output and temporary the scan named,
// through its door, which checks again. One line counts what happened, and a
// failure never fails the run, because the next full walk finds the output
// again.
func (r *closeRun) sweepOrphans(now time.Time) {
	if r.board == nil {
		return
	}
	data, err := r.board.readNote(orphansNote)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	var note orphansNoteBody
	if err == nil {
		err = json.Unmarshal(data, &note)
	}
	if err != nil {
		r.logf("could not read the outputs of gone videos the scan found: %v", err)
		return
	}
	var outputs, temporaries, kept, refused int
	count := func(removed bool, err error, tally *int) {
		switch {
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			refused++
		case removed:
			*tally++
		default:
			kept++
		}
	}
	for _, output := range note.Outputs {
		var removed bool
		var err error
		switch output.Kind {
		case orphanTiles:
			removed, err = r.writer.removeOrphanTiles(r.root, r.kind, output.Title, output.Entry)
		case orphanRecords:
			removed, err = r.writer.removeOrphanRecords(r.root, r.kind, output.Title, output.Entry)
		default:
			err = fmt.Errorf("no door takes the kind %q", output.Kind)
		}
		count(removed, err, &outputs)
	}
	for _, path := range note.Temporaries {
		if !filepath.IsLocal(path) {
			count(false, fmt.Errorf("refusing %s: it is outside the root", path), &temporaries)
			continue
		}
		removed, err := r.writer.removeStaleTemporary(filepath.Join(r.root, path), now)
		count(removed, err, &temporaries)
	}
	r.logf("removed %d outputs of gone videos and %d stale temporaries, kept %d, and could not remove %d",
		outputs, temporaries, kept, refused)
}
