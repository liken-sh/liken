// The model: what one coverage input says about one source tree.
//
// Two formats reach this program, and a repository may hand it both
// at once. A Go profile counts statements, and a Cobertura report
// counts lines. The two numbers answer different questions, so
// nothing here adds them together. Each input keeps its own unit,
// and the page says which unit every number is in.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Mark says what the tests did with one line of source.
type Mark int

const (
	// Unmarked is a line the coverage data says nothing about: a
	// comment, a blank line, or a declaration.
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

// File is one source file's coverage. Covered and Total are in the
// unit of the Source that holds the file. Text is the file as it
// stands in the tree, and is empty when the tree does not hold it.
type File struct {
	Name    string
	Covered int
	Total   int
	Marks   map[int]Mark
	Text    string
}

// mark colors one line. A line that any test reached is covered,
// whatever else the data says about it. Two blocks meet on the line
// that opens a branch, and one of them is often the branch nothing
// ran. That line still ran, so the covered mark wins.
func (f *File) mark(line int, m Mark) {
	if f.Marks[line] == Covered {
		return
	}
	f.Marks[line] = m
}

// Source is one input file, read.
type Source struct {
	Label   string
	Unit    string
	Covered int
	Total   int
	Files   []File
}

// Read reads one coverage input and resolves the files it names
// against root, the tree the input describes.
//
// The format comes from the content and not from the name, because a
// repository names these files what it likes. A Go profile always
// opens with its mode line, and a Cobertura report is XML with one
// coverage element.
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

// sortedFiles puts the files in name order, which is the order a
// reader looks for one in.
func sortedFiles(byName map[string]*File) []File {
	files := make([]File, 0, len(byName))
	for _, file := range byName {
		files = append(files, *file)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files
}

// readText returns the first of the candidate paths that holds a
// file. A path that holds nothing is not an error: the numbers for a
// file are still true where its text is missing, and a report built
// on one machine from another machine's data still counts.
func readText(candidates ...string) string {
	for _, candidate := range candidates {
		if text, err := os.ReadFile(candidate); err == nil {
			return string(text)
		}
	}
	return ""
}

// percent is the one place a ratio becomes a number a reader sees.
// A file with nothing to count is complete, not empty, so it reads
// as 100 percent.
func percent(covered, total int) string {
	if total == 0 {
		return "100.0%"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(covered)/float64(total))
}
