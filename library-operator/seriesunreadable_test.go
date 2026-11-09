package main

// what these tests read: a season folder the series walk cannot read marks
// the walk incomplete, so the prune does not delete the episodes it holds.

import (
	"os"
	"path/filepath"
	"testing"
)

// The walk keeps the episodes of the seasons it read before the one it
// could not read, and the incomplete pass holds the prune back.
func TestAnUnreadableSeasonFolderMarksTheSeriesWalkIncomplete(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory of mode 000")
	}
	root := t.TempDir()
	show := filepath.Join(root, "Show (2019)")
	writeFile(t, filepath.Join(show, "Season 01", "Show S01E01.mkv"), "video")
	unreadableFolder(t, show, "Season 02")

	result := walkSeries(root, "house/series", nil)

	if !result.readError {
		t.Error("an unreadable season folder left the series walk complete")
	}
	if len(result.episodes) != 1 || result.episodes[0].Season != 1 {
		t.Errorf("episodes = %+v, want the one the readable season holds", result.episodes)
	}
}
