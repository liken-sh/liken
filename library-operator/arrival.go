package main

// arrival.go is the arrival ledger as the walk reads it: the time a video
// file arrived, kept in .liken/arrival.yaml beside the files by the arrival
// fact in arrivalfact.go. The time is the inode change time and never the
// modification time, because an importer rewrites the modification time to
// the release date, and user space cannot set the change time, so the
// importer's rewrite stamps it with the real moment of import. The walk
// reads and never writes, because the scan Job mounts the volume read-only.
// This file holds the ledger's shape, its read, the per-folder read the
// walk makes, the change time, and the earliest-of fold.

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// The ledger file's name is the arrival fact's own, likenLedgerName of
// factArrival, so the fact's entries and its attempts are one file with one
// writer.
const arrivalLedgerName = "arrival.yaml"

// The ledger's shape: one entry per video file the folder holds.
type arrivalLedger struct {
	Files []arrivalEntry `yaml:"files"`
}

// One entry: the file's path relative to the folder, and the time the file
// arrived, RFC 3339 in UTC.
type arrivalEntry struct {
	Path string    `yaml:"path"`
	At   time.Time `yaml:"at"`
}

// The walk's read of the entries alone, through the fact ledger reader, so
// the attempts beside them are read by the .liken pass and not here. A
// folder with no ledger reads as an empty ledger.
func readArrivalLedger(folder string) (arrivalLedger, error) {
	ledger, err := readLikenLedger(folder, factArrival)
	if err != nil {
		return arrivalLedger{}, err
	}
	return arrivalLedger{Files: ledger.Files}, nil
}

// What the walk reads for one video. added is the ledger's time, or the
// change time where the ledger holds none. arrived is the ledger's time
// alone, and zero where it holds none, which is what the arrival fact's gap
// reads.
type fileArrival struct {
	added   int64
	arrived int64
}

// The arrival of each video in a folder, read and never written. A ledger
// that cannot be read is an error the caller marks the pass incomplete
// with, and the change times stand for that pass. The change time is read
// only for a file with no entry, so a re-walk of a settled folder stats
// nothing here.
func folderArrivals(dir string, videos []string) (map[string]fileArrival, error) {
	ledger, readErr := readArrivalLedger(dir)
	held := map[string]int64{}
	for _, entry := range ledger.Files {
		held[entry.Path] = entry.At.Unix()
	}
	arrivals := map[string]fileArrival{}
	var statErr error
	for _, video := range videos {
		if at, known := held[video]; known {
			arrivals[video] = fileArrival{added: at, arrived: at}
			continue
		}
		at, err := changeTime(filepath.Join(dir, video))
		if err != nil {
			statErr = errors.Join(statErr, err)
			continue
		}
		arrivals[video] = fileArrival{added: at}
	}
	if readErr != nil {
		return arrivals, readErr
	}
	return arrivals, statErr
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
