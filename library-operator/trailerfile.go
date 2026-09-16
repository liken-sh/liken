package main

// trailerfile.go is the vocabulary of the trailerfile fact: the ledger entry,
// the gap, the pick of a trailer and of one of its files, and the name the
// pulled file lands under.

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
)

// The one file this fact pulled, as the ledger records it.
type trailerFileEntry struct {
	Provider string `yaml:"provider"`
	Key      string `yaml:"key"`
	URL      string `yaml:"url"`
	// The file the pull landed, relative to the title's own folder.
	File   string    `yaml:"file"`
	Height int       `yaml:"height,omitempty"`
	Size   int64     `yaml:"size,omitempty"`
	At     time.Time `yaml:"at"`
}

// The folder a pulled trailer lands in, which is the folder the walk already
// reads every video under as a trailer, and the container the remux writes.
const (
	trailersFolderName   = extrasTrailers
	trailerFileExtension = ".mp4"
)

// The height the pick reads as the feature's where the catalog states none.
const trailerFeatureHeight = 1080

// The characters a trailer's own name keeps beside letters and digits.
const trailerNameKept = " -.()[]"

// How long a landed file's name runs at most, in characters.
const trailerNameLimit = 80

// The name one trailer lands under. A name that keeps nothing lands under the
// role itself, and a leading dot is cut because the walk skips a dot name.
func safeTrailerName(name string) string {
	var kept strings.Builder
	for _, letter := range name {
		if unicode.IsLetter(letter) || unicode.IsDigit(letter) ||
			strings.ContainsRune(trailerNameKept, letter) {
			kept.WriteRune(letter)
			continue
		}
		kept.WriteRune(' ')
	}
	safe := strings.Join(strings.Fields(kept.String()), " ")
	if runes := []rune(safe); len(runes) > trailerNameLimit {
		safe = string(runes[:trailerNameLimit])
	}
	if safe = strings.Trim(safe, " ."); safe == "" {
		return fileRoleTrailer
	}
	return safe
}

// The order the pick reads a title's trailers in: score, then height, then
// the earliest published date.
func sortTrailerRows(rows []trailerRow) []trailerRow {
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, func(a, b trailerRow) int {
		if order := cmp.Compare(b.Score, a.Score); order != 0 {
			return order
		}
		if order := cmp.Compare(b.Resolution, a.Resolution); order != 0 {
			return order
		}
		return cmp.Compare(a.Published, b.Published)
	})
	return sorted
}

// The trailer this fact pulls: the first of that order whose site it can
// fetch from.
func pickTrailerRow(rows []trailerRow, sources map[string]trailerSource) (trailerRow, bool) {
	for _, row := range sortTrailerRows(rows) {
		if _, held := sources[row.Site]; held {
			return row, true
		}
	}
	return trailerRow{}, false
}

// The file the pull takes: the tallest no taller than the feature, and the
// shortest above it where none fits. A file whose height the site does not
// state counts as fitting. A file the site states is over the limit is left
// where it is, because the pull would fail on it.
func pickTrailerFile(files []trailerFile, ceiling int) (trailerFile, bool) {
	var fits, above trailerFile
	held, over := false, false
	for _, file := range files {
		if file.Size > trailerPullLimit {
			continue
		}
		if file.Height > ceiling {
			if !over || file.Height < above.Height {
				above, over = file, true
			}
			continue
		}
		if !held || file.Height > fits.Height {
			fits, held = file, true
		}
	}
	if held {
		return fits, true
	}
	return above, over
}

// The same list as a quoted SQL set.
func quotedSites(sites []string) string {
	quoted := make([]string, len(sites))
	for at, site := range sites {
		quoted[at] = "'" + site + "'"
	}
	return strings.Join(quoted, ",")
}

// Every identified title of one library, with the folder that holds its files.
// A title whose path is the library root is left out, because the pull writes
// a trailers folder inside the title's own folder, and a title at the root has
// no folder of its own.
const identifiedTitleFolders = `SELECT library, id, path FROM movies ` +
	`WHERE id NOT LIKE 'movie:path:%' AND path NOT IN ('', '.') ` +
	`UNION ALL SELECT library, id, path FROM series ` +
	`WHERE id NOT LIKE 'series:path:%' AND path NOT IN ('', '.')`

// The titles that already hold a present trailer file under their own folder.
// The query reads the folder off a join by library and never off the outer
// row, so SQLite builds it once. It scopes by a range over the path, so a
// folder name that holds a LIKE metacharacter reads its own files alone.
func titlesWithATrailerFile() string {
	return `SELECT folders.id FROM (` + identifiedTitleFolders + `) AS folders ` +
		`JOIN files ON files.library = folders.library AND ` +
		pathScopeBounds("files.path", "folders.path",
			`folders.path || '/'`, `folders.path || '0'`) + ` ` +
		`WHERE folders.library = ?1 AND files.present = 1 ` +
		`AND files.role = '` + fileRoleTrailer + `'`
}

// The gap: an identified title with a trailers row this fact can fetch and no
// present trailer file under the title's own path. So a hand-placed trailer
// beside the feature closes it.
func trailerFileGapQuery() string {
	held := `id IN (SELECT item FROM trailers WHERE trailers.library = ?1 ` +
		`AND site IN (` + quotedSites(trailerFetchSites()) + `)) ` +
		`AND id NOT IN (` + titlesWithATrailerFile() + `)`
	return `SELECT id FROM (` + identifiedTitleFolders + `) AS titles ` +
		`WHERE library = ?1 AND ` + gapClause(factTrailerFile, "id", held)
}

// The trailers of one title, which the pick sorts.
const trailersOfQuery = `SELECT provider, key, site, url, name, kind, published, resolution, score ` +
	`FROM trailers WHERE library = ? AND item = ?`

func (c *Catalog) trailersOf(ctx context.Context, library, item string) ([]trailerRow, error) {
	var rows []trailerRow
	err := c.stream(ctx, trailersOfQuery, []any{library, item}, func(cells []any) error {
		if len(cells) < 9 {
			return nil
		}
		row := trailerRow{Library: library, Item: item}
		row.Provider, _ = cells[0].(string)
		row.Key, _ = cells[1].(string)
		row.Site, _ = cells[2].(string)
		row.URL, _ = cells[3].(string)
		row.Name, _ = cells[4].(string)
		row.Kind, _ = cells[5].(string)
		row.Published, _ = cells[6].(string)
		row.Resolution = int(cellNumber(cells[7]))
		row.Score = int(cellNumber(cells[8]))
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the trailers of %s: %w", item, err)
	}
	return rows, nil
}

// The ceiling the file pick reads: the tallest feature the title holds. The
// query scopes by a range over the path, so a folder name that holds a LIKE
// metacharacter reads its own files alone.
func featureHeightQuery() string {
	return `SELECT max(height) FROM files WHERE library = ? ` +
		`AND present = 1 AND role = '` + fileRolePrimary + `' AND ` + pathScopeClause("path")
}

// A title whose feature the probe has not read, and a title with no feature
// at all, both read as the default height.
func (c *Catalog) featureHeight(ctx context.Context, library, path string) (int, error) {
	tallest := 0
	params := append([]any{library}, pathScopeParams(path)...)
	err := c.stream(ctx, featureHeightQuery(), params, func(cells []any) error {
		if len(cells) > 0 {
			tallest = int(cellNumber(cells[0]))
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("reading the feature of %s: %w", path, err)
	}
	if tallest <= 0 {
		return trailerFeatureHeight, nil
	}
	return tallest, nil
}
