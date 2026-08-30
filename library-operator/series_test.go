package main

// These tests walk the testdata series tree, so the series
// item, the season and episode numbers, the episode file, and the
// episode's own aliases are proved against real files.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWalkSeries(t *testing.T) {
	result := walkSeries("testdata/series", "house/series", nil)

	if result.titles != 1 {
		t.Errorf("titles = %d, want the one series", result.titles)
	}
	if len(result.series) != 1 {
		t.Fatalf("series rows = %d, want 1", len(result.series))
	}
	series := result.series[0]
	if series.Id != "series:tvdb:81189" {
		t.Errorf("series id = %q, want series:tvdb:81189", series.Id)
	}
	if series.Title != "Breaking Bad" || series.Slug != "breaking-bad-2008" {
		t.Errorf("series identity = %q %q", series.Title, series.Slug)
	}

	if len(result.episodes) != 1 {
		t.Fatalf("episode rows = %d, want 1", len(result.episodes))
	}
	episode := result.episodes[0]
	if episode.Id != "episode:tvdb:81189:s02e05" {
		t.Errorf("episode id = %q, want episode:tvdb:81189:s02e05", episode.Id)
	}
	if episode.Season != 2 || episode.Episode != 5 {
		t.Errorf("placement = s%02de%02d, want s02e05", episode.Season, episode.Episode)
	}
	if episode.Series != series.Id {
		t.Errorf("episode series = %q, want %q", episode.Series, series.Id)
	}
	if episode.Title != "Breakage" || episode.Released != "2009-04-05" {
		t.Errorf("episode identity = %q %q", episode.Title, episode.Released)
	}
	if episode.Art == "" {
		t.Error("the episode has no thumbnail recorded")
	}
}

func TestWalkSeriesEpisodeFile(t *testing.T) {
	result := walkSeries("testdata/series", "house/series", nil)
	file, held := fileByItem(result, "episode:tvdb:81189:s02e05")
	if !held {
		t.Fatal("the episode has no file linked")
	}
	if file.Width != 1280 || file.Height != 720 || file.VideoCodec != "h264" || file.AudioCodec != "ac3" {
		t.Errorf("attributes = %dx%d %s/%s, want the episode streamdetails", file.Width, file.Height, file.VideoCodec, file.AudioCodec)
	}
	if file.Path != filepath.Join("Breaking Bad", "Season 02", "Breaking Bad - S02E05.mkv") {
		t.Errorf("path = %q, want the episode file relative to the root", file.Path)
	}
}

func TestWalkSeriesEmitsEpisodeAlias(t *testing.T) {
	result := walkSeries("testdata/series", "house/series", nil)
	found := false
	for _, alias := range result.aliases {
		if alias.Alias == "episode:tvdb:340124" && alias.Item == "episode:tvdb:81189:s02e05" {
			found = true
		}
	}
	if !found {
		t.Error("the episode's own tvdb id does not resolve the episode item")
	}
}

// An episode file the scanner cannot number has no place under the
// series, so the walk leaves it out rather than guessing.
func TestWalkSeriesSkipsAnUnnumberedEpisode(t *testing.T) {
	root := t.TempDir()
	seriesDir := filepath.Join(root, "Mystery Show")
	seasonDir := filepath.Join(seriesDir, "Season 01")
	writeFile(t, filepath.Join(seriesDir, "tvshow.nfo"), `<tvshow><title>Mystery Show</title><uniqueid type="tvdb">1</uniqueid></tvshow>`)
	writeFile(t, filepath.Join(seasonDir, "S01E01.mkv"), "video")
	writeFile(t, filepath.Join(seasonDir, "Bonus Feature.mkv"), "video")

	result := walkSeries(root, "house/series", nil)
	if len(result.episodes) != 1 {
		t.Errorf("episodes = %d, want only the numbered one", len(result.episodes))
	}
}

// A season folder names the season where an episode sidecar carries
// only its episode number.
func TestWalkSeriesTakesSeasonFromTheFolder(t *testing.T) {
	root := t.TempDir()
	seasonDir := filepath.Join(root, "Show", "Season 03")
	writeFile(t, filepath.Join(root, "Show", "tvshow.nfo"), `<tvshow><title>Show</title><uniqueid type="tvdb">9</uniqueid></tvshow>`)
	writeFile(t, filepath.Join(seasonDir, "ep.mkv"), "video")
	writeFile(t, filepath.Join(seasonDir, "ep.nfo"), `<episodedetails><title>Seven</title><episode>7</episode></episodedetails>`)

	result := walkSeries(root, "house/series", nil)
	if len(result.episodes) != 1 {
		t.Fatalf("episodes = %d, want 1", len(result.episodes))
	}
	if result.episodes[0].Season != 3 || result.episodes[0].Episode != 7 {
		t.Errorf("placement = s%02de%02d, want s03e07 from the folder and the sidecar", result.episodes[0].Season, result.episodes[0].Episode)
	}
}

// The ignore list skips a folder at the series root and one inside a
// series, so neither a recycle bin beside the series nor a staging
// folder within it reaches the catalog.
func TestWalkSeriesSkipsIgnoredFolders(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Show", "tvshow.nfo"), `<tvshow><title>Show</title><uniqueid type="tvdb">9</uniqueid></tvshow>`)
	writeFile(t, filepath.Join(root, "Show", "Season 01", "Show S01E01.mkv"), "video")
	writeFile(t, filepath.Join(root, "Show", "#recycle", "Show S09E09.mkv"), "video")
	writeFile(t, filepath.Join(root, "#recycle", "Other", "Other S01E01.mkv"), "video")

	result := walkSeries(root, "house/series", ignoreSet{"#recycle": true})
	if result.titles != 1 {
		t.Fatalf("titles = %d, want only the series outside the recycle bin", result.titles)
	}
	if len(result.episodes) != 1 {
		t.Errorf("episodes = %d, want only the one below the season folder", len(result.episodes))
	}
	if result.episodes[0].Episode != 1 {
		t.Errorf("episode = %d, want the numbered one, not the staged file", result.episodes[0].Episode)
	}
}

// writeFile writes a fixture file and the directories above it, so a
// test builds a small volume in a temporary directory.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWalkSeriesOnAMissingRoot(t *testing.T) {
	if result := walkSeries("testdata/nowhere", "house/series", nil); result.titles != 0 {
		t.Errorf("titles = %d, want an empty walk", result.titles)
	}
}

// A series folder with no tvshow.nfo takes its identity from the
// folder name: a name with a year is identified, one without is counted
// unidentified and still cataloged. A top-level episode file with a
// marker in its name is placed with no season folder and no sidecar.
func TestWalkSeriesFallsBackToFolderNames(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Show (2010)", "The Show S01E01.mkv"), "video")
	writeFile(t, filepath.Join(root, "Nameless", "Nameless S03E02.mkv"), "video")

	result := walkSeries(root, "house/series", nil)
	if result.titles != 2 {
		t.Fatalf("titles = %d, want both series", result.titles)
	}
	if result.unidentified != 1 {
		t.Errorf("unidentified = %d, want the one with no year", result.unidentified)
	}
	byID := map[string]episodeRow{}
	for _, episode := range result.episodes {
		byID[episode.Id] = episode
	}
	placed, held := byID["episode:path:the-show-2010:s01e01"]
	if !held || placed.Season != 1 || placed.Episode != 1 {
		t.Errorf("top-level episode = %+v, want s01e01 from the file name", placed)
	}
}
