package solver

// indexSet tracks the index files in the index directory and the series
// that GET /indexes returns for them. indexwatch.go updates it as files
// appear and disappear. When the list of files changes, indexSet
// rewrites the engine's configuration and wakes the clients that watch
// GET /indexes. The engine reads its configuration at the start of each
// solve, so the next solve uses the new files.

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"sync"
)

type indexSet struct {
	// config is the path of the engine's configuration file.
	config   string
	cpuLimit int
	logs     io.Writer

	mu    sync.Mutex
	files map[string]IndexFile
	// written is the sorted list of files in the last configuration
	// written. loaded is false until the first write, so the first change
	// writes the configuration even when the list is empty.
	written []IndexFile
	loaded  bool
	series  Indexes
	// changed is closed, and replaced, at each change to the series.
	changed chan struct{}
}

func newIndexSet(config string, cpuLimit int, logs io.Writer) *indexSet {
	return &indexSet{config: config, cpuLimit: cpuLimit, logs: logs, files: map[string]IndexFile{}, series: Indexes{}, changed: make(chan struct{})}
}

// current returns the series and a channel that closes when it changes.
func (s *indexSet) current() (Indexes, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.series, s.changed
}

// replace sets the files to the result of a listing of the whole
// directory.
func (s *indexSet) replace(files []IndexFile) error {
	return s.change(func(byPath map[string]IndexFile) {
		clear(byPath)
		for _, file := range files {
			byPath[file.Path] = file
		}
	})
}

func (s *indexSet) put(file IndexFile) error {
	return s.change(func(byPath map[string]IndexFile) { byPath[file.Path] = file })
}

func (s *indexSet) drop(path string) error {
	return s.change(func(byPath map[string]IndexFile) { delete(byPath, path) })
}

// change applies edit to the files. When the sorted list of files is
// different after the edit, it rewrites the engine's configuration,
// logs the new series, and wakes the clients that watch the series.
func (s *indexSet) change(edit func(map[string]IndexFile)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	edit(s.files)
	files := slices.SortedFunc(maps.Values(s.files), func(a, b IndexFile) int { return cmp.Compare(a.Path, b.Path) })
	if s.loaded && slices.Equal(files, s.written) {
		return nil
	}
	if err := writeConfig(s.config, EngineConfig(files, s.cpuLimit)); err != nil {
		return err
	}
	s.written, s.loaded = files, true
	s.series = Series(files)
	if s.series == nil {
		s.series = Indexes{}
	}
	close(s.changed)
	s.changed = make(chan struct{})
	for _, one := range s.series {
		fmt.Fprintf(s.logs, "index series %d: %d files, quads of %g to %g arcminutes\n", one.ID, one.Files, one.ScaleArcmin.Low, one.ScaleArcmin.High)
	}
	if len(s.series) == 0 {
		fmt.Fprintln(s.logs, "no index-*.fits file to read, so every solve returns no solution")
	}
	return nil
}

// writeConfig writes to a temporary file and renames it over path, so an
// engine that starts during the write reads the old file or the new one,
// never a partial file.
func writeConfig(path, config string) error {
	next := path + ".next"
	if err := os.WriteFile(next, []byte(config), 0o600); err != nil {
		return err
	}
	return os.Rename(next, path)
}
