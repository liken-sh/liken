package worker

// The worker's operations on the outbox: reporting its free space and
// its frames, and deleting a centering frame after its solve. The
// worker's pod mounts the outbox read-write only so that it can delete
// centering frames, and the worker deletes nothing else.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/liken-sh/liken/astrophotography-operator/outbox"
)

// ErrNoSuchFrame is the error for a centering frame that is not in the
// outbox.
var ErrNoSuchFrame = errors.New("no such centering frame")

// State returns the free bytes of the outbox's file system and the
// paths of the frames in the targets' directories, in path order.
func (e Extractor) State() (OutboxResponse, error) {
	var stat syscall.Statfs_t
	// Do not wrap the cause. The server maps fs.ErrNotExist to 404,
	// which means a missing frame, and a missing mount must be a 500.
	if err := syscall.Statfs(e.Outbox, &stat); err != nil {
		return OutboxResponse{}, fmt.Errorf("reading the free space of the outbox %s: %v", e.Outbox, err)
	}
	response := OutboxResponse{FreeBytes: int64(stat.Bavail) * stat.Bsize, Frames: []string{}}
	err := filepath.WalkDir(e.Outbox, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, _ := filepath.Rel(e.Outbox, path)
		if frame, err := outbox.Parse(filepath.ToSlash(relative)); err == nil && frame.Target != "" {
			response.Frames = append(response.Frames, frame.Path())
		}
		return nil
	})
	return response, err
}

// RemoveCentering deletes the centering frame of one FRAMEID from the
// _centering directory of every camera and reservation. The FRAMEID
// goes into a Glob pattern, so the check accepts only a plain file name
// with none of the characters that Glob reads as a pattern or an
// escape.
func (e Extractor) RemoveCentering(frameID string) error {
	if !filepath.IsLocal(frameID) || filepath.Base(frameID) != frameID || frameID == "." || strings.ContainsAny(frameID, `\*?[`) {
		return fmt.Errorf("%w: %q is not a FRAMEID", ErrBadRequest, frameID)
	}
	matches, _ := filepath.Glob(filepath.Join(e.Outbox, "*", "*", outbox.Centering, frameID+outbox.Extension))
	if len(matches) == 0 {
		return fmt.Errorf("%w: %s", ErrNoSuchFrame, frameID)
	}
	for _, path := range matches {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
