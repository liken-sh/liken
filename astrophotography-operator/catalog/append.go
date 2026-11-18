package catalog

// The promotion Job is the only writer of a catalog. It appends a
// frame's line before the frame's outbox copy is deleted: after it
// verifies a copied frame in the archive, or before the rename when
// the outbox and the archive are one claim. So the line must
// survive a power cut once Append returns, and Append syncs the file,
// and the directory when it created the file.

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Append writes line at the end of the catalog at name, and creates
// the file and its directory when they do not exist. A crash during an
// earlier append can leave a last line with no newline. Append then
// writes a newline first, so the new line starts on a line of its own
// and the reader skips only the cut one.
func Append(name string, line Line) error {
	// A Line holds only strings and numbers, so Marshal cannot fail.
	encoded, _ := json.Marshal(line)
	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	_, statErr := os.Stat(name)
	created := errors.Is(statErr, os.ErrNotExist)
	file, err := os.OpenFile(name, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	cut, err := endsCut(file)
	if err == nil {
		if cut {
			encoded = append([]byte("\n"), encoded...)
		}
		_, err = file.Write(append(encoded, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil && created {
		err = SyncDir(dir)
	}
	return err
}

// endsCut reports whether the file is not empty and its last byte is
// not a newline.
func endsCut(file *os.File) (bool, error) {
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return false, err
	}
	last := make([]byte, 1)
	if _, err := file.ReadAt(last, info.Size()-1); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return last[0] != '\n', nil
}

// SyncDir syncs dir, so that a file created, renamed, or removed in it
// survives a power cut.
func SyncDir(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = file.Sync()
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}
