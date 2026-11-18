// This file reads a Go coverage profile, the file that `go test
// -coverprofile` writes.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

// profileRow matches one line of a profile, which describes one basic
// block: its start, its end, its number of statements, and the number
// of times the tests ran it.
//
//	github.com/liken-sh/liken/media-operator/edl/album.go:40.13,43.22 3 0
var profileRow = regexp.MustCompile(`^(.+):(\d+)\.\d+,(\d+)\.\d+ (\d+) (\d+)$`)

// readProfile counts statements, not lines, because go-test-coverage
// counts statements. So the report page shows the same coverage that
// the coverage check enforces.
func readProfile(data []byte, root string) (*Source, error) {
	module, err := modulePath(root)
	if err != nil {
		return nil, err
	}
	source := &Source{Label: "Go (" + module + ")", Unit: "statements"}
	byName := map[string]*File{}

	for number, line := range strings.Split(string(data), "\n") {
		// The first line is the "mode:" header.
		if number == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		row := profileRow.FindStringSubmatch(line)
		if row == nil {
			return nil, fmt.Errorf("line %d of the coverage profile is not a block of the form file:line.column,line.column statements count: %q", number+1, line)
		}
		start, _ := strconv.Atoi(row[2])
		end, _ := strconv.Atoi(row[3])
		statements, _ := strconv.Atoi(row[4])
		count, _ := strconv.Atoi(row[5])

		// A profile names a file by its import path, which is the
		// module path followed by the file's path in the module.
		name := strings.TrimPrefix(row[1], module+"/")
		file := byName[name]
		if file == nil {
			file = &File{
				Name:  name,
				Marks: map[int]Mark{},
				Text:  readText(filepath.Join(root, name)),
			}
			byName[name] = file
		}

		mark := Uncovered
		if count > 0 {
			mark = Covered
			file.Covered += statements
			source.Covered += statements
		}
		file.Total += statements
		source.Total += statements
		for at := start; at <= end; at++ {
			file.mark(at, mark)
		}
	}

	source.Files = sortedFiles(byName)
	return source, nil
}

// modulePath returns the module path in root's go.mod. Every profile
// row starts with it, and readProfile removes it to find the file
// under root. A root with no go.mod is a wrong -root, so this returns
// an error instead of a report with no source text.
func modulePath(root string) (string, error) {
	name := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	module := modfile.ModulePath(data)
	if module == "" {
		return "", fmt.Errorf("%s has no module directive", name)
	}
	return module, nil
}
