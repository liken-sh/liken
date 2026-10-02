package main

// stalepartials.go removes the partial files that stopped writers leave under
// a title's .liken directory. Every writer there writes a file beside its
// final name and renames it into place: the operator's own temporaries carry
// likenTempMark, and the appearances tool names its partial file
// <name>.partial-<host>-<pid>. A writer that is killed before the rename
// leaves its partial file, and a killed detect pass can leave several
// megabytes. Nothing else removes it, because a run removes only the partial
// files of its own pod.
//
// The walk already lists each title's .liken directory, so it finds these
// files with no extra read of the title folder. The scan container mounts the
// volume read-only, so the walk cannot remove them. It writes their paths on
// the Job's phases volume, and the close container, which mounts the volume
// read-write, removes each one through volumeWriter after the scan ends.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// How long a partial file must go unchanged before the sweep removes it. A
// writer changes its partial file as it writes, so the age counts from the
// last write and not from the start. The longest writer is the appearances
// tool's detect pass, which decodes a whole feature and which
// appearancesDetectTimeout stops after 6 hours. The ledgers and the matches
// file take seconds. A day is four times the longest writer, so the sweep
// never removes a file that a live writer still holds.
const stalePartialAge = 24 * time.Hour

// The directory under .liken where the appearances tool writes its detections
// records and their partial files.
const appearancesRecordsDirectory = "appearances"

// The note the scan writes on the phases volume: the paths of the stale
// partial files, relative to the root, as one JSON array.
const stalePartialsNote = "stale-partials.json"

// Whether a file name carries either partial mark.
func isPartialName(name string) bool {
	return strings.Contains(name, likenTempMark) || isToolPartialName(name)
}

// Whether a name ends in the appearances tool's partial mark: .partial-, a
// host name, a hyphen, and a process id. A host name holds hyphens, so the
// process id is what follows the last one.
func isToolPartialName(name string) bool {
	_, mark, found := strings.Cut(name, ".partial-")
	if !found {
		return false
	}
	at := strings.LastIndexByte(mark, '-')
	if at <= 0 || at == len(mark)-1 {
		return false
	}
	return allDigits(mark[at+1:])
}

// Whether a directory is a .liken directory or the appearances directory
// under one, which are the two directories the sweep reads.
func isUnderLiken(dir string) bool {
	if filepath.Base(dir) == appearancesRecordsDirectory {
		dir = filepath.Dir(dir)
	}
	return filepath.Base(dir) == likenDirectory
}

// Whether a file last written at modified has gone unchanged for
// stalePartialAge.
func isStale(modified, now time.Time) bool {
	return now.Sub(modified) > stalePartialAge
}

// The stale partial files among one directory's entries. The listing gives
// each name, and only a name with a partial mark costs a stat, so a .liken
// directory with no partial file costs nothing more than its listing.
func stalePartialsIn(dir string, entries []os.DirEntry, now time.Time) []string {
	var stale []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !isPartialName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !isStale(info.ModTime(), now) {
			continue
		}
		stale = append(stale, filepath.Join(dir, entry.Name()))
	}
	return stale
}

// The stale partial files of the appearances directory under one .liken
// directory. This is the one extra directory read of the sweep, and only a
// title the appearances fact has worked holds the directory. A listing that
// fails finds nothing, because a missed partial file waits for the next walk
// and is no reason to mark the walk incomplete.
func staleRecordPartials(liken string, now time.Time) []string {
	records := filepath.Join(liken, appearancesRecordsDirectory)
	entries, err := os.ReadDir(records)
	if err != nil {
		return nil
	}
	return stalePartialsIn(records, entries, now)
}

// The scan's half: the paths the walk found, relative to the root, written on
// the phases volume for the close container. A walk that found none writes
// nothing.
func (s *scanner) noteStalePartials() {
	if s.board == nil || len(s.stalePartials) == 0 {
		return
	}
	relative := make([]string, 0, len(s.stalePartials))
	for _, path := range s.stalePartials {
		relative = append(relative, relativePath(s.root, path))
	}
	data, _ := json.Marshal(relative)
	if err := s.board.writeNote(stalePartialsNote, data); err != nil {
		s.logf("could not hand the %d stale partial files to the close container: %v", len(relative), err)
	}
}

// The close container's half: each path the scan noted, removed where it is
// still a stale partial file. The remove reads the file's age again, so a
// file that a writer changed after the walk read it stays. A failure is
// logged and never fails the run, because the next walk finds the file again.
func (r *closeRun) sweepStalePartials(now time.Time) {
	if r.board == nil {
		return
	}
	data, err := r.board.readNote(stalePartialsNote)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	var relative []string
	if err == nil {
		err = json.Unmarshal(data, &relative)
	}
	if err != nil {
		r.logf("could not read the stale partial files the scan found: %v", err)
		return
	}
	for _, path := range relative {
		if !filepath.IsLocal(path) {
			r.logf("refusing to remove %s: it is outside the root", path)
			continue
		}
		absolute := filepath.Join(r.root, path)
		removed, err := r.writer.removeStalePartial(absolute, now)
		switch {
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			r.logf("could not remove the stale partial file %s: %v", r.named(absolute), err)
		case removed:
			r.logf("removed the stale partial file %s, which no writer had changed for %s",
				r.named(absolute), stalePartialAge)
		}
	}
}
