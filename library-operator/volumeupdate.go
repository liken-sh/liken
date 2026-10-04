package main

// volumeupdate.go is the door for a file a writer reads, changes, and writes
// back: a .liken ledger, an .nfo file whose one element a fact edits, and a
// person's contributor.yaml. Two clusters can mount one library volume, and
// each runs its own Jobs over it, so two writers can read one file at once.
// The temporary and the rename keep each write whole, but the second rename
// would drop the change the first one made. So the door reads the file again
// just before its rename, and where another writer changed it in between, it
// applies its change to what that writer left. The window that remains is
// the moment between that read and the rename, not the length of a provider
// call or a decode. Closing it would take a lock that every cluster honours,
// and the volume offers none.

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// How many times the door applies its change before it gives up. A file
// that another writer changes on every try is a file under heavy contention,
// and an error the caller records is better than a write made from bytes
// the file no longer holds.
const updateTries = 5

var errUpdateRaced = errors.New("another writer changed the file on every try")

// The file as one read found it: its bytes, and whether it was there at all,
// because an empty file and no file are two states a change tells apart.
type fileState struct {
	data   []byte
	exists bool
}

func readFileState(path string) (fileState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fileState{}, nil
	}
	if err != nil {
		return fileState{}, err
	}
	return fileState{data: data, exists: true}, nil
}

func (s fileState) same(other fileState) bool {
	return s.exists == other.exists && bytes.Equal(s.data, other.data)
}

// Whether the file is there and holds exactly these bytes, which is when a
// write of them would change nothing but the file's modification time.
func (s fileState) holds(data []byte) bool {
	return s.exists && bytes.Equal(s.data, data)
}

// The read-change-write door. change takes the file's bytes, nil where there
// is no file, and answers the bytes to land, or nil to leave the file as it
// is. A change that answers the bytes the file already holds leaves it as it
// is too, for the reason write gives. The answer is the bytes the file holds
// when the door is done.
func (w *volumeWriter) update(target string, change func(current []byte) ([]byte, error)) ([]byte, error) {
	current, err := readFileState(target)
	if err != nil {
		return nil, err
	}
	for range updateTries {
		next, err := change(current.data)
		if err != nil {
			return nil, err
		}
		if next == nil || current.holds(next) {
			return current.data, nil
		}
		temporary := w.temporary(target)
		if err := w.stage(temporary, next); err != nil {
			return nil, err
		}
		now, err := readFileState(target)
		if err != nil {
			_ = w.removeTemporary(temporary)
			return nil, err
		}
		if now.same(current) {
			if err := w.land(temporary, target); err != nil {
				_ = w.removeTemporary(temporary)
				return nil, err
			}
			return next, nil
		}
		if err := w.removeTemporary(temporary); err != nil {
			return nil, err
		}
		current = now
	}
	return nil, fmt.Errorf("writing %s: %w", target, errUpdateRaced)
}
