package main

// An apply pod writes to the machine only after the operator sets the
// pod's annotation firmware.liken.sh/write to go. The pod reads its
// annotations from a downward API volume, so it needs no API
// credentials and no network. The kubelet writes all the annotations to
// one file, and replaces the file by writing a new copy and switching a
// symbolic link to it, so a reader never reads a partial file.
//
// The wait opens an inotify watch on the volume's directory before it
// reads the file. A signal written before the read is in the file, and
// a signal written after it produces a watch event. Reading first could
// miss a signal written between the read and the watch.

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

const (
	writeAnnotation = "firmware.liken.sh/write"
	writeGo         = "go"
	annotationsFile = "annotations"
)

// watcher is a watch on a directory. changes receives a value after the
// directory changes, and is closed when the watch fails.
type watcher struct {
	changes <-chan struct{}
	close   func() error
}

// awaitSignal returns nil once the annotations in dir contain the
// signal. When the watch fails or ctx ends, it returns an error, and the
// apply reports a failed result with nothing written.
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
				return fmt.Errorf("the watch of %s ended before the signal arrived", dir)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// readSignal reports whether the annotations in dir contain the signal.
// The kubelet writes one key="value" line for each annotation, with the
// value quoted as a Go string. A volume that the kubelet has not filled
// yet has no annotations file, and so no signal.
func readSignal(dir string) (bool, error) {
	contents, err := os.ReadFile(filepath.Join(dir, annotationsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	lines := bufio.NewScanner(bytes.NewReader(contents))
	for lines.Scan() {
		key, quoted, _ := strings.Cut(lines.Text(), "=")
		if value, err := strconv.Unquote(quoted); key == writeAnnotation && err == nil && value == writeGo {
			return true, nil
		}
	}
	return false, nil
}
