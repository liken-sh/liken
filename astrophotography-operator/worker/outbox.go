package worker

// The worker's answers about the outbox: the space left, the frames of
// the targets, and the removal of a centering frame after its solve.
// The worker mounts the outbox read and write for that removal alone,
// and deletes nothing else.

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

// ErrNoSuchFrame is the error of a centering frame that the outbox
// does not hold.
var ErrNoSuchFrame = errors.New("no such centering frame")

// State answers the free bytes of the outbox's file system and every
// frame in a target's directory, in the order of their paths.
func (e Extractor) State() (OutboxAnswer, error) {
	var stat syscall.Statfs_t
	// The error does not wrap the cause, so an outbox that is not
	// mounted answers 500, not the 404 of a frame that is missing.
	if err := syscall.Statfs(e.Outbox, &stat); err != nil {
		return OutboxAnswer{}, fmt.Errorf("statfs %s: %v", e.Outbox, err)
	}
	answer := OutboxAnswer{FreeBytes: int64(stat.Bavail) * stat.Bsize, Frames: []string{}}
	err := filepath.WalkDir(e.Outbox, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, _ := filepath.Rel(e.Outbox, path)
		if frame, err := outbox.Parse(filepath.ToSlash(relative)); err == nil && frame.Target != "" {
			answer.Frames = append(answer.Frames, frame.Path())
		}
		return nil
	})
	return answer, err
}

// RemoveCentering deletes the centering frame of one FRAMEID from every
// camera's and reservation's directory that holds it.
func (e Extractor) RemoveCentering(frameID string) error {
	if frameID == "" || frameID == "." || frameID == ".." || strings.ContainsAny(frameID, `/\*?[`) {
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
