package solver

// The index set is the files that the engine reads and the series that
// GET /indexes answers. indexwatch.go changes it as files come and go,
// and each change writes the engine's configuration again and wakes
// every client that follows the series. The engine reads its
// configuration at the start of each solve, so the next solve after a
// change reads the new set.

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
	// config is the path of the engine's configuration, and cpuLimit
	// the limit it gives the engine.
	config   string
	cpuLimit int
	logs     io.Writer

	mu sync.Mutex
	// files holds each index file by its path.
	files map[string]IndexFile
	// written is the list of files that config names, and loaded is
	// false until the first write.
	written []IndexFile
	loaded  bool
	series  Indexes
	// changed closes at the next change of the files.
	changed chan struct{}
}

func newIndexSet(config string, cpuLimit int, logs io.Writer) *indexSet {
	return &indexSet{config: config, cpuLimit: cpuLimit, logs: logs, files: map[string]IndexFile{}, series: Indexes{}, changed: make(chan struct{})}
}

// current answers the series, and a channel that closes when they
// change.
func (s *indexSet) current() (Indexes, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.series, s.changed
}

// replace sets the whole set, after a read of the whole directory.
func (s *indexSet) replace(files []IndexFile) error {
	return s.change(func(held map[string]IndexFile) {
		clear(held)
		for _, file := range files {
			held[file.Path] = file
		}
	})
}

// put adds a file, or replaces the one at the same path.
func (s *indexSet) put(file IndexFile) error {
	return s.change(func(held map[string]IndexFile) { held[file.Path] = file })
}

// drop removes the file at a path, when the set holds one.
func (s *indexSet) drop(path string) error {
	return s.change(func(held map[string]IndexFile) { delete(held, path) })
}

// change applies an edit to the files. When the list of files changed,
// it writes the configuration, logs the series, and wakes the
// followers.
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
		fmt.Fprintln(s.logs, "no index-*.fits to read, so every solve answers no solution")
	}
	return nil
}

// writeConfig replaces the configuration by a rename, so an engine
// that starts during the write reads the old file or the new one,
// never a part of one.
func writeConfig(path, config string) error {
	next := path + ".next"
	if err := os.WriteFile(next, []byte(config), 0o600); err != nil {
		return err
	}
	return os.Rename(next, path)
}
