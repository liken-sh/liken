// A Go profile counts statements and a Cobertura report counts lines,
// so the totals of two inputs cannot be added. Each Source keeps its
// own Unit, and the report shows the unit beside every number.

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Mark records whether the tests ran one line of source.
type Mark int

const (
	// Unmarked is a line that the coverage data does not mention, such
	// as a comment, a blank line, or a declaration.
	Unmarked Mark = iota
	Covered
	Uncovered
)

func (m Mark) String() string {
	switch m {
	case Covered:
		return "covered"
	case Uncovered:
		return "uncovered"
	}
	return "unmarked"
}

// File is the coverage of one source file. Covered and Total use the
// unit of the Source that contains the file. Text is the file's content
// in the tree, or empty when the tree does not have the file.
type File struct {
	Name    string
	Covered int
	Total   int
	Marks   map[int]Mark
	Text    string
}

// mark sets the mark of one line, but never replaces Covered. In a Go
// profile, the line that opens a branch ends one block and starts the
// next. When the first block ran and the branch did not, the line ran,
// so it stays Covered.
func (f *File) mark(line int, m Mark) {
	if f.Marks[line] == Covered {
		return
	}
	f.Marks[line] = m
}

// Source is the parsed contents of one coverage input file.
type Source struct {
	Label   string
	Unit    string
	Covered int
	Total   int
	Files   []File
}

// Read parses one coverage input and reads the source files it lists
// from root. It detects the format from the content and not from the
// file name, because repositories name these files differently.
func Read(path, root string) (*Source, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	switch {
	case bytes.HasPrefix(data, []byte("mode: ")):
		return readProfile(data, root)
	case bytes.Contains(data, []byte("<coverage")):
		return readCobertura(data, filepath.Base(path), root)
	}
	return nil, fmt.Errorf("%s is neither a Go coverage profile nor a Cobertura report", path)
}

// sortedFiles returns the files in name order, so a reader can find
// one in the report's table.
func sortedFiles(byName map[string]*File) []File {
	files := make([]File, 0, len(byName))
	for _, file := range byName {
		files = append(files, *file)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files
}

// readText returns the content of the first candidate path that can
// be read, or an empty string when none can. A missing file is not an
// error, because the input can come from another machine whose source
// tree is not here, and its numbers are still valid.
func readText(candidates ...string) string {
	for _, candidate := range candidates {
		if text, err := os.ReadFile(candidate); err == nil {
			return string(text)
		}
	}
	return ""
}

// percent shows a total of zero as 100.0%, because nothing is left to
// cover.
func percent(covered, total int) string {
	if total == 0 {
		return "100.0%"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(covered)/float64(total))
}
