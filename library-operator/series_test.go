package main

// These tests walk the testdata series tree, so the series
// item, the season and episode numbers, the episode file, and the
// episode's own aliases are proved against real files.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

// A file directly in a series folder links to the series. A file in a season
// folder links to the episode whose own name it starts with. A season poster
// matches no episode, so it links to the series.
func TestWalkSeriesReadsEveryFileTheSeriesCarries(t *testing.T) {
	result := walkSeries("testdata/series", "house/series", nil)
	files := filesByPath(result)
	season := filepath.Join("Breaking Bad", "Season 02")

	cases := []struct {
		path         string
		wantType     string
		wantRole     string
		wantLanguage string
		wantItem     string
	}{
		{
			path:     filepath.Join("Breaking Bad", "tvshow.nfo"),
			wantType: fileTypeMetadata, wantRole: fileRoleTVShow, wantItem: "series:tvdb:81189",
		},
		{
			path:     filepath.Join("Breaking Bad", "folder.jpg"),
			wantType: fileTypeImage, wantRole: fileRolePoster, wantItem: "series:tvdb:81189",
		},
		{
			path:     filepath.Join(season, "season02-poster.jpg"),
			wantType: fileTypeImage, wantRole: fileRolePoster, wantItem: "series:tvdb:81189",
		},
		{
			path:     filepath.Join(season, "Breaking Bad - S02E05.mkv"),
			wantType: fileTypeVideo, wantRole: fileRolePrimary, wantItem: "episode:tvdb:81189:s02e05",
		},
		{
			path:     filepath.Join(season, "Breaking Bad - S02E05.nfo"),
			wantType: fileTypeMetadata, wantRole: fileRoleEpisode, wantItem: "episode:tvdb:81189:s02e05",
		},
		{
			path:     filepath.Join(season, "Breaking Bad - S02E05-thumb.jpg"),
			wantType: fileTypeImage, wantRole: fileRoleThumb, wantItem: "episode:tvdb:81189:s02e05",
		},
		{
			path:     filepath.Join(season, "Breaking Bad - S02E05.en.srt"),
			wantType: fileTypeSubtitle, wantRole: fileRoleFull, wantLanguage: "en", wantItem: "episode:tvdb:81189:s02e05",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			row, held := files[testCase.path]
			if !held {
				t.Fatalf("the walk read no row for %s", testCase.path)
			}
			if row.Type != testCase.wantType || row.Role != testCase.wantRole || row.Language != testCase.wantLanguage {
				t.Errorf("class = %s/%s/%s, want %s/%s/%s",
					row.Type, row.Role, row.Language, testCase.wantType, testCase.wantRole, testCase.wantLanguage)
			}
			if row.Items[0] != testCase.wantItem {
				t.Errorf("items = %v, want %q", row.Items, testCase.wantItem)
			}
			if row.Modified == 0 {
				t.Errorf("modified = %d, want the time the walk's own stat read", row.Modified)
			}
		})
	}
}

// An extras folder under a series holds the trailers and the featurettes.
// Each of them links to the series, because none belongs to one episode.
func TestWalkSeriesReadsAnExtrasFolder(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Show")
	writeFile(t, filepath.Join(show, "tvshow.nfo"), `<tvshow><title>Show</title><uniqueid type="tvdb">9</uniqueid></tvshow>`)
	writeFile(t, filepath.Join(show, "Season 01", "Show S01E01.mkv"), "video")
	writeFile(t, filepath.Join(show, "Trailers", "Show teaser.mkv"), "video")

	result := walkSeries(root, "house/series", nil)
	row, held := filesByPath(result)[filepath.Join("Show", "Trailers", "Show teaser.mkv")]
	if !held {
		t.Fatalf("the walk read no row for the trailer, it read %v", filesByPath(result))
	}
	if row.Type != fileTypeVideo || row.Role != fileRoleTrailer {
		t.Errorf("class = %s/%s, want a video trailer", row.Type, row.Role)
	}
	if row.Items[0] != "series:tvdb:9" {
		t.Errorf("items = %v, want the series", row.Items)
	}
}

// A video in a season folder that the scanner cannot number belongs to no
// episode. It links to the series, so the catalog holds it rather than
// losing it.
func TestWalkSeriesCatalogsAnUnnumberedVideoUnderTheSeries(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Show")
	writeFile(t, filepath.Join(show, "tvshow.nfo"), `<tvshow><title>Show</title><uniqueid type="tvdb">9</uniqueid></tvshow>`)
	writeFile(t, filepath.Join(show, "Season 01", "Show S01E01.mkv"), "video")
	writeFile(t, filepath.Join(show, "Season 01", "Bonus Feature.mkv"), "video")

	result := walkSeries(root, "house/series", nil)
	row, held := filesByPath(result)[filepath.Join("Show", "Season 01", "Bonus Feature.mkv")]
	if !held {
		t.Fatal("the walk read no row for the unnumbered video")
	}
	if row.Items[0] != "series:tvdb:9" {
		t.Errorf("items = %v, want the series, because no episode claims it", row.Items)
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

// doubleEpisodeVolume writes a series folder holding one file of two episodes,
// with the sidecar the test gives it, and reports the season folder.
func doubleEpisodeVolume(t *testing.T, root, video, sidecar string) string {
	t.Helper()
	show := filepath.Join(root, "Coastline")
	season := filepath.Join(show, "Season 04")
	writeFile(t, filepath.Join(show, "tvshow.nfo"), `<tvshow><title>Coastline</title><uniqueid type="tvdb">7</uniqueid></tvshow>`)
	writeFile(t, filepath.Join(season, video), "video")
	if sidecar != "" {
		writeFile(t, filepath.Join(season, stripExtension(video)+".nfo"), sidecar)
	}
	return season
}

// episodesByID indexes a walk's episode rows by item id.
func episodesByID(result *walkResult) map[string]episodeRow {
	index := map[string]episodeRow{}
	for _, episode := range result.episodes {
		index[episode.Id] = episode
	}
	return index
}

// A file named for two episodes is two episode items and one file row, and the
// row names both items. The three range forms name the same pair.
func TestWalkSeriesReadsADoubleEpisodeFile(t *testing.T) {
	cases := []struct {
		name  string
		video string
	}{
		{name: "a dash and a second marker", video: "Coastline - S04E10-E11 - The Long Way.mkv"},
		{name: "a dash and a bare number", video: "Coastline - S04E10-11 - The Long Way.mkv"},
		{name: "a second marker with no dash", video: "Coastline - S04E10E11 - The Long Way.mkv"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			doubleEpisodeVolume(t, root, testCase.video, "")

			result := walkSeries(root, "house/series", nil)
			if len(result.episodes) != 2 {
				t.Fatalf("episodes = %d, want both the file names", len(result.episodes))
			}
			for index, number := range []int{10, 11} {
				episode := result.episodes[index]
				wantID := fmt.Sprintf("episode:tvdb:7:s04e%02d", number)
				if episode.Id != wantID || episode.Season != 4 || episode.Episode != number {
					t.Errorf("episode = %q s%02de%02d, want %q", episode.Id, episode.Season, episode.Episode, wantID)
				}
			}

			path := filepath.Join("Coastline", "Season 04", testCase.video)
			rows := 0
			for _, row := range result.files {
				if row.Path == path {
					rows++
				}
			}
			if rows != 1 {
				t.Fatalf("file rows = %d, want the one file the two episodes play", rows)
			}
			want := []string{"episode:tvdb:7:s04e10", "episode:tvdb:7:s04e11"}
			if items := filesByPath(result)[path].Items; !reflect.DeepEqual(items, want) {
				t.Errorf("items = %v, want %v", items, want)
			}
		})
	}
}

// A sidecar of two episodedetails blocks gives each episode its own title and
// plot, in the order the sidecar wrote them.
func TestWalkSeriesReadsAMultiEpisodeSidecar(t *testing.T) {
	root := t.TempDir()
	doubleEpisodeVolume(t, root, "Coastline - S04E10-E11.mkv",
		`<episodedetails><title>The Long Way Down</title><season>4</season><episode>10</episode><plot>The crew descends.</plot></episodedetails>
<episodedetails><title>The Long Way Back</title><season>4</season><episode>11</episode><plot>The crew climbs.</plot></episodedetails>`)

	episodes := episodesByID(walkSeries(root, "house/series", nil))
	first, held := episodes["episode:tvdb:7:s04e10"]
	if !held {
		t.Fatalf("the walk read no first episode, it read %v", episodes)
	}
	second, held := episodes["episode:tvdb:7:s04e11"]
	if !held {
		t.Fatalf("the walk read no second episode, it read %v", episodes)
	}
	if first.Title != "The Long Way Down" || first.Body.Plot != "The crew descends." {
		t.Errorf("first episode = %q %q, want the first block", first.Title, first.Body.Plot)
	}
	if second.Title != "The Long Way Back" || second.Body.Plot != "The crew climbs." {
		t.Errorf("second episode = %q %q, want the second block", second.Title, second.Body.Plot)
	}
	if first.Path != second.Path || first.Added != second.Added {
		t.Errorf("file facts = %q %d and %q %d, want the one file's own", first.Path, first.Added, second.Path, second.Added)
	}
}

// A sidecar of one block titles the first episode of the range. The second
// takes the numbered title, because the block describes the first episode and
// no episode carries another episode's title.
func TestWalkSeriesNumbersTheSecondEpisodeOfASingleBlockSidecar(t *testing.T) {
	root := t.TempDir()
	doubleEpisodeVolume(t, root, "Coastline - S04E10-E11.mkv",
		`<episodedetails><title>The Long Way Down</title><season>4</season><episode>10</episode><plot>The crew descends.</plot></episodedetails>`)

	episodes := episodesByID(walkSeries(root, "house/series", nil))
	first := episodes["episode:tvdb:7:s04e10"]
	second, held := episodes["episode:tvdb:7:s04e11"]
	if !held {
		t.Fatalf("the walk read no second episode, it read %v", episodes)
	}
	if first.Title != "The Long Way Down" {
		t.Errorf("first title = %q, want the block's own", first.Title)
	}
	if second.Title != "S04E11" {
		t.Errorf("second title = %q, want the numbered title", second.Title)
	}
	if second.Body.Plot != "" {
		t.Errorf("second plot = %q, want none, because the block describes the first episode", second.Body.Plot)
	}
}

// A range wider than maxRangeEpisodes is a name the scanner does not read as a
// range, so a strange name mints one item and not a run of them.
func TestWalkSeriesRefusesAWideEpisodeRange(t *testing.T) {
	root := t.TempDir()
	doubleEpisodeVolume(t, root, "Coastline - S04E10-E40.mkv", "")

	result := walkSeries(root, "house/series", nil)
	if len(result.episodes) != 1 {
		t.Fatalf("episodes = %d, want the first alone", len(result.episodes))
	}
	if result.episodes[0].Id != "episode:tvdb:7:s04e10" {
		t.Errorf("episode = %q, want the first of the range", result.episodes[0].Id)
	}
}

// The files beside a double-episode video link to the same two episodes the
// video links to.
func TestWalkSeriesLinksASubtitleToBothEpisodes(t *testing.T) {
	root := t.TempDir()
	season := doubleEpisodeVolume(t, root, "Coastline - S04E10-E11.mkv", "")
	writeFile(t, filepath.Join(season, "Coastline - S04E10-E11.en.srt"), "subtitle")

	path := filepath.Join("Coastline", "Season 04", "Coastline - S04E10-E11.en.srt")
	row, held := filesByPath(walkSeries(root, "house/series", nil))[path]
	if !held {
		t.Fatal("the walk read no row for the subtitle")
	}
	want := []string{"episode:tvdb:7:s04e10", "episode:tvdb:7:s04e11"}
	if !reflect.DeepEqual(row.Items, want) {
		t.Errorf("items = %v, want %v", row.Items, want)
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

// A series folder with no tvshow.nfo falls back to the folder name, and
// the walk is complete.
func TestASeriesWithNoSidecarKeepsThePathIdentity(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Show (2019)", "Season 01", "Show S01E01.mkv"), "video")

	result := walkSeries(root, "house/series", nil)

	if result.readError {
		t.Error("a series with no sidecar marked the walk incomplete")
	}
	if len(result.series) != 1 || result.series[0].Id != "series:path:show-2019" {
		t.Errorf("series = %+v, want the path-derived id", result.series)
	}
}

// A tvshow.nfo the scanner cannot read marks the walk incomplete, for
// the reason the movies walk does: the fall-back would change the series
// id, and the sweep would then delete every episode under the old one.
func TestAnUnreadableSeriesSidecarMarksTheWalkIncomplete(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Show (2019)")
	writeFile(t, filepath.Join(show, "Season 01", "Show S01E01.mkv"), "video")
	if err := os.MkdirAll(filepath.Join(show, "tvshow.nfo"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := walkSeries(root, "house/series", nil)

	if !result.readError {
		t.Error("a series sidecar that could not be read left the walk complete")
	}
}

// An episode sidecar that cannot be read marks the walk incomplete too,
// so an episode whose numbers the walk could not read is not swept as
// departed.
func TestAnUnreadableEpisodeSidecarMarksTheWalkIncomplete(t *testing.T) {
	root := t.TempDir()
	season := filepath.Join(root, "Show (2019)", "Season 01")
	writeFile(t, filepath.Join(season, "Show S01E01.mkv"), "video")
	if err := os.MkdirAll(filepath.Join(season, "Show S01E01.nfo"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := walkSeries(root, "house/series", nil)

	if !result.readError {
		t.Error("an episode sidecar that could not be read left the walk complete")
	}
}
