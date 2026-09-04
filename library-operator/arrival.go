package main

// arrival.go is the arrival ledger: the time the walk first saw each video
// file, kept in .liken/arrival.yaml beside the files. An importer rewrites a
// file's modification time to the title's release date, so that time says
// nothing about when the file arrived. The inode change time does: user space
// cannot set it, so the importer's rewrite stamps it with the real moment of
// import. The ledger makes that moment durable against the next sweep that
// touches the file. This file holds the ledger's shape, its read, the recorder
// that adds entries and writes the file, and the change time read.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// The walk's file under .liken/. It is not named for a fact, so the fact
// ledger read never opens it.
const arrivalLedgerName = "arrival.yaml"

// The ledger's shape: one entry per video file the folder holds.
type arrivalLedger struct {
	Files []arrivalEntry `yaml:"files"`
}

// One entry: the file's path relative to the folder, and the time the walk
// first saw it, RFC 3339 in UTC.
type arrivalEntry struct {
	Path string    `yaml:"path"`
	At   time.Time `yaml:"at"`
}

// A folder with no ledger reads as an empty ledger, because the first walk of
// a folder starts from nothing.
func readArrivalLedger(folder string) (arrivalLedger, error) {
	data, err := os.ReadFile(filepath.Join(folder, likenDirectory, arrivalLedgerName))
	if errors.Is(err, fs.ErrNotExist) {
		return arrivalLedger{}, nil
	}
	if err != nil {
		return arrivalLedger{}, err
	}
	var ledger arrivalLedger
	if err := yaml.Unmarshal(data, &ledger); err != nil {
		return arrivalLedger{}, err
	}
	return ledger, nil
}

// The walk's writer of arrival ledgers. One recorder serves a whole walk, so
// a volume that refuses the write is logged once and not once per folder. A
// nil recorder reads the ledger and never writes it. That is a fact's re-read
// of a folder, because only the walk writes this file.
type arrivalRecorder struct {
	writer  *volumeWriter
	logf    func(format string, args ...any)
	mutex   sync.Mutex
	refused bool
}

func newArrivalRecorder(writer *volumeWriter, logf func(format string, args ...any)) *arrivalRecorder {
	return &arrivalRecorder{writer: writer, logf: logf}
}

// The arrival of each video in the folder. An entry that exists is kept as it
// is. A file with none gets its change time, and the file is written only when
// an entry was added. A ledger that cannot be read is an error the caller marks
// the pass incomplete with; the change times stand for that pass, and the file
// is left as it was. The change time is read only for a file with no entry, so
// a re-walk of a settled folder stats nothing here.
func (r *arrivalRecorder) arrivals(dir string, videos []string) (map[string]int64, error) {
	ledger, readErr := readArrivalLedger(dir)
	held := map[string]int64{}
	for _, entry := range ledger.Files {
		held[entry.Path] = entry.At.Unix()
	}
	var statErr error
	added := false
	for _, video := range videos {
		if _, known := held[video]; known {
			continue
		}
		at, err := changeTime(filepath.Join(dir, video))
		if err != nil {
			statErr = errors.Join(statErr, err)
			continue
		}
		held[video] = at
		ledger.Files = append(ledger.Files, arrivalEntry{Path: video, At: time.Unix(at, 0).UTC()})
		added = true
	}
	if readErr != nil {
		return held, readErr
	}
	if added {
		r.record(dir, ledger)
	}
	return held, statErr
}

// The one write, through the volume's write door. A write the volume refuses
// is logged once per recorder, and the walk goes on with the change times it
// already holds.
func (r *arrivalRecorder) record(dir string, ledger arrivalLedger) {
	if r == nil || r.writer == nil {
		return
	}
	data, err := yaml.Marshal(ledger)
	if err == nil {
		err = r.writer.writeInto(filepath.Join(dir, likenDirectory), arrivalLedgerName, data)
	}
	if err == nil {
		return
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.refused {
		return
	}
	r.refused = true
	r.logf("could not write the arrival ledger at %s, using change times for this walk: %v", dir, err)
}

// The inode change time, which is the real moment of import. User space
// cannot set it, so an importer that rewrites the modification time leaves it
// alone. This is the Linux stat, which is where the scanner runs.
func changeTime(path string) (int64, error) {
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		return 0, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	return stat.Ctim.Sec, nil
}

// The earlier of two arrivals, where zero means none is known. A series or a
// set takes the first arrival among its members, and a member with none does
// not pull it to zero.
func earliestArrival(held, candidate int64) int64 {
	if candidate == 0 {
		return held
	}
	if held == 0 || candidate < held {
		return candidate
	}
	return held
}
