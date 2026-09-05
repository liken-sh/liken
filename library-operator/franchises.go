package main

// franchises.go reads a checkout of a franchises repository into the three
// franchise tables, the way movies.go reads a title folder. The checkout
// holds one directory per franchise, each with a franchise.yaml and the
// franchise's art beside it under Kodi's names. The walk reads no volume
// and no other library, because a member is a provider alias the member's
// own library writes. A file the schema refuses is counted unidentified and
// skipped, and the files beside it still write their rows.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// scopeFranchise is the word that leads every franchise id, beside the
// scopes in rows.go.
const scopeFranchise = "franchise"

// franchiseFileName is the one file the scanner reads out of a franchise
// directory. The AGENTS.md beside it is for the next author, and the art
// is for the enrichers.
const franchiseFileName = "franchise.yaml"

// franchiseBody is what the page draws around the wall, as the franchises
// row's body. The names are the file's own, so a reader of the row and a
// reader of the file read one vocabulary.
type franchiseBody struct {
	Universe string             `json:"universe,omitempty"`
	Calendar *franchiseCalendar `json:"calendar,omitempty"`
	Eras     []franchiseEra     `json:"eras,omitempty"`
	Sources  []string           `json:"sources,omitempty"`
}

// franchiseRow is one row of the franchises item table: the item header
// every kind carries, with the franchise's clock in the body. Released is
// empty, because the scanner reads no member's date.
type franchiseRow struct {
	Id       string
	Library  string
	Kind     string
	Path     string
	Title    string
	SortKey  string
	Slug     string
	Released string
	Added    int64
	Art      string
	Arts     []string
	Duration int64
	Body     franchiseBody
}

// franchiseMemberRow is one entry of one franchise's order, at its position
// in story order. Alias is the member as the provider alias its own library
// writes, and it is the whole join to the catalog. Universes is a JSON
// list, empty where the entry is in the home universe alone. ReleaseYear is
// the file's own, and 0 where the file gives none.
type franchiseMemberRow struct {
	Library   string
	Franchise string
	Position  int
	Kind      string
	Alias     string
	Title     string
	Released  string
	// ReleaseYear is the year of that date, its first four characters, so
	// the wall reads one integer for its label and never parses a string.
	ReleaseYear int
	Timed       int
	TimeFrom    float64
	TimeTo      float64
	Universes   string
}

// franchiseRunRow is one season, or one episode, of one series run.
// Episode 0 is the whole season, and a run with no rows is the whole show.
type franchiseRunRow struct {
	Library   string
	Franchise string
	Position  int
	Season    int
	Episode   int
}

// franchiseID derives the id of one franchise from the name of its
// directory. The file carries no provider id, so the directory name is the
// identity, and a renamed directory is a new row.
func franchiseID(directory string) string {
	return scopeFranchise + ":name:" + slug(directory, 0)
}

// walkFranchises reads a whole checkout into one walkResult, one directory
// at a time. The directories are read in name order, so two walks of one
// commit write the same rows in the same order. A directory with no
// franchise.yaml is not a franchise, which is how the repository's own
// .git directory is passed over. The checkout holds the franchise.yaml
// files, and the claim holds the art the scan downloaded from the links
// each one carries; a franchise's directory carries the same name in both.
func walkFranchises(checkout, artRoot, library string) *walkResult {
	result := &walkResult{}
	names, err := franchiseDirectories(checkout)
	if err != nil {
		result.noteReadError(err)
		return result
	}
	for _, name := range names {
		scanFranchiseDirectory(checkout, artRoot, name, library, result)
	}
	return result
}

// franchiseDirectories are the directories of one checkout, in name order, so
// two walks of one commit read them the same way. A dot name is passed over,
// which is how the repository's own .git directory is left out.
func franchiseDirectories(checkout string) ([]string, error) {
	entries, err := os.ReadDir(checkout)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, entry := range entries {
		if entry.IsDir() && !skipName(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// readFranchiseFile reads and validates one directory's franchise.yaml. A
// directory that holds no franchise.yaml is not a franchise, and it returns no
// file and no error.
func readFranchiseFile(checkout, name string) (*franchiseFile, error) {
	data, err := os.ReadFile(filepath.Join(checkout, name, franchiseFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errFranchiseUnreadable, err)
	}
	return parseFranchiseFile(data)
}

// errFranchiseUnreadable tells a file the scanner could not read from a file
// the schema refuses. The first marks the pass incomplete, and the second is
// reported and skipped.
var errFranchiseUnreadable = errors.New("the scanner could not read the file")

// scanFranchiseDirectory reads one franchise directory into its rows. A
// file the schema refuses leaves the directory counted unidentified and
// named, the same reporting path a folder no sidecar identifies takes. A
// file the scanner cannot read marks the pass incomplete, so a checkout it
// could not read never sweeps the rows the catalog holds.
func scanFranchiseDirectory(checkout, artRoot, name, library string, result *walkResult) {
	file, err := readFranchiseFile(checkout, name)
	if err != nil {
		// A file the scanner could read and the schema refuses is a fault
		// in the file; a file it could not read is a fault in the checkout.
		if errors.Is(err, errFranchiseUnreadable) {
			result.noteReadError(err)
			return
		}
		result.unidentified++
		result.unidentifiedNames = append(result.unidentifiedNames, name)
		return
	}
	if file == nil {
		return
	}

	id := franchiseID(name)
	// The art is read off the claim, the way every other kind reads it, so the
	// columns hold paths under the library root and never links.
	primaryArt, allArt, err := franchiseArt(artRoot, name)
	result.noteReadError(err)

	result.franchises = append(result.franchises, franchiseRow{
		Id:      id,
		Library: library,
		Kind:    libraryKindFranchises,
		Path:    name,
		Title:   file.Name,
		SortKey: sortKey(file.Name),
		Slug:    slug(file.Name, 0),
		Art:     primaryArt,
		Arts:    allArt,
		Body: franchiseBody{
			Universe: file.Universe,
			Calendar: file.Calendar,
			Eras:     file.Eras,
			Sources:  file.Sources,
		},
	})
	appendFranchiseOrder(library, id, file, result)
	result.titles++
}

// appendFranchiseOrder walks the order and writes one member row per
// entry, from position 1. A series with seasons is one member row and one
// run row per season or episode, so The Clone Wars is one slot on the wall
// and not thirty.
func appendFranchiseOrder(library, franchise string, file *franchiseFile, result *walkResult) {
	for index, entry := range file.Order {
		position := index + 1
		result.franchiseMembers = append(result.franchiseMembers,
			franchiseMemberRow{
				Library:     library,
				Franchise:   franchise,
				Position:    position,
				Kind:        entry.kind(),
				Alias:       entry.kind() + ":" + entry.reference(),
				Title:       entry.Title,
				Released:    entry.Released,
				ReleaseYear: entry.releaseYear(),
				Timed:       timedMark(entry.Time),
				TimeFrom:    spanEnd(entry.Time, func(t franchiseTime) *float64 { return t.From }),
				TimeTo:      spanEnd(entry.Time, func(t franchiseTime) *float64 { return t.To }),
				Universes:   universesValue(entry.Universes),
			})
		for _, season := range entry.Seasons {
			result.franchiseRuns = append(result.franchiseRuns,
				franchiseRunRows(library, franchise, position, season)...)
		}
	}
}

// franchiseRunRows are the run rows of one season: the whole season, or
// one row per episode the file names. A range expands here, because the
// file expands with no catalog, and a held-episode count is then one join.
func franchiseRunRows(library, franchise string, position int, season franchiseSeason) []franchiseRunRow {
	row := franchiseRunRow{Library: library, Franchise: franchise, Position: position, Season: *season.Season}
	if len(season.Episodes) == 0 {
		return []franchiseRunRow{row}
	}
	rows := []franchiseRunRow{}
	for _, code := range season.Episodes {
		// The file validated before the walk read it, so every code
		// expands and the error here cannot happen.
		episodes, _ := expandEpisodeCode(code)
		for _, episode := range episodes {
			rows = append(rows, franchiseRunRow{
				Library: library, Franchise: franchise, Position: position,
				Season: episode.Season, Episode: episode.Episode,
			})
		}
	}
	return rows
}

// timedMark is 1 where the entry carries a time, and 0 where it carries
// none.
func timedMark(span *franchiseTime) int {
	if span == nil {
		return 0
	}
	return 1
}

// spanEnd is one end of an entry's span, and 0 where the entry has no
// time. The file validated before the walk read it, so a span the walk
// reads carries both ends.
func spanEnd(span *franchiseTime, end func(franchiseTime) *float64) float64 {
	if span == nil {
		return 0
	}
	if value := end(*span); value != nil {
		return *value
	}
	return 0
}

// universesValue is the entry's universes as the JSON list the column
// holds. An entry that names none is in the home universe alone, which is
// the empty list.
func universesValue(universes []string) string {
	if len(universes) == 0 {
		return "[]"
	}
	payload, _ := json.Marshal(universes)
	return string(payload)
}
