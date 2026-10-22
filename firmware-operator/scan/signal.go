package main

// An apply pod waits on its machine for the operator's signal to write.
// The operator sends it by setting the pod's annotation
// firmware.liken.sh/write to go, and the pod reads its own annotations
// through a downward API volume, so it needs no API credentials and no
// network. The kubelet writes the annotations into the volume as one
// file, and changes it by writing a new copy and swapping a symbolic
// link to it, so a reader never sees half a file.
//
// The wait follows the repository's rule for a source with no version
// to resume from: open the watch of the volume's directory first, and
// then read the file once. A signal that arrives between the two is
// in the file, and one that arrives after the read wakes the watch.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The annotation that carries the signal, its value, and the file the
// downward API volume holds the pod's annotations in.
const (
	writeAnnotation = "firmware.liken.sh/write"
	writeGo         = "go"
	annotationsFile = "annotations"
)

// watcher is a watch of a directory. changes receives a value when the
// directory changed, at most one for each read of the events, and
// closes when the watch fails.
type watcher struct {
	changes <-chan struct{}
	close   func() error
}

// awaitSignal returns nil once the volume in dir holds the signal, and
// an error when the wait ends without it. A watch that fails ends the
// wait, so the pod ends Failed with nothing written, and the operator
// reads that answer.
func awaitSignal(ctx context.Context, dir string, open func(dir string) (watcher, error)) error {
	w, err := open(dir)
	if err != nil {
		return fmt.Errorf("watching %s: %w", dir, err)
	}
	defer w.close()
	for {
		signalled, err := readSignal(dir)
		if err != nil || signalled {
			return err
		}
		select {
		case _, open := <-w.changes:
			if !open {
				return fmt.Errorf("the watch of %s ended", dir)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// readSignal reports whether the annotations in dir hold the signal.
// The kubelet writes one key="value" line for each annotation, with the
// value quoted as Go quotes a string. A volume with no file yet holds
// no signal.
func readSignal(dir string) (bool, error) {
	held, err := os.ReadFile(filepath.Join(dir, annotationsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	lines := bufio.NewScanner(bytes.NewReader(held))
	for lines.Scan() {
		key, quoted, _ := strings.Cut(lines.Text(), "=")
		if value, err := strconv.Unquote(quoted); key == writeAnnotation && err == nil && value == writeGo {
			return true, nil
		}
	}
	return false, nil
}
