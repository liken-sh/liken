// The reader for a Go coverage profile, the file `go test
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

// A profile row is one basic block: where it starts, where it ends,
// how many statements it holds, and how many times the tests ran it.
//
//	github.com/liken-sh/media-operator/edl/album.go:40.13,43.22 3 0
var profileRow = regexp.MustCompile(`^(.+):(\d+)\.\d+,(\d+)\.\d+ (\d+) (\d+)$`)

// readProfile counts statements, not lines. That is what the Go
// toolchain measures, and it is what go-test-coverage reports as the
// total test coverage, so the number on the page is the number the
// gate enforces. A line count over the same profile would be a
// second, different number for the same tests.
func readProfile(data []byte, root string) (*Source, error) {
	module, err := modulePath(root)
	if err != nil {
		return nil, err
	}
	source := &Source{Label: "Go (" + module + ")", Unit: "statements"}
	byName := map[string]*File{}

	for number, line := range strings.Split(string(data), "\n") {
		if number == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		row := profileRow.FindStringSubmatch(line)
		if row == nil {
			return nil, fmt.Errorf("line %d of the profile is not a block: %s", number+1, line)
		}
		start, _ := strconv.Atoi(row[2])
		end, _ := strconv.Atoi(row[3])
		statements, _ := strconv.Atoi(row[4])
		count, _ := strconv.Atoi(row[5])

		// The profile names a file by its import path, which starts
		// with the module path. The tree holds it under the rest.
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

// modulePath reads the tree's module path, which is the prefix every
// profile row carries. Without it the rows name no file in the tree,
// so a tree with no module file is a wrong -root and not a report
// with missing sources.
func modulePath(root string) (string, error) {
	name := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	module := modfile.ModulePath(data)
	if module == "" {
		return "", fmt.Errorf("%s declares no module path", name)
	}
	return module, nil
}
