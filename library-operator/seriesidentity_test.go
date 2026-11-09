package main

// what these tests read: the identity the series walk gives a folder whose
// name holds no title.

import (
	"path/filepath"
	"testing"
)

// A series folder named by its provider id alone has no title in its name,
// so the series takes the folder's name as its title, and the id identifies
// it.
func TestASeriesFolderNamedByItsIDAloneKeepsTheNameAsItsTitle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "[tvdbid-800002]", "Season 01", "S01E01.mkv"), "video")

	result := walkSeries(root, "house/series", nil)

	if len(result.series) != 1 || result.series[0].Id != "series:tvdb:800002" ||
		result.series[0].Title != "[tvdbid-800002]" {
		t.Errorf("series = %+v, want the tvdb id and the folder's name as the title", result.series)
	}
	if result.unidentified != 0 {
		t.Errorf("unidentified = %d, want none for a folder with a provider id", result.unidentified)
	}
}
