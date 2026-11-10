package main

// What these tests read: how the episode .nfo beside a double-episode file
// and the range in its name together number the episodes the file holds.

import (
	"path/filepath"
	"reflect"
	"testing"
)

// Each block of an episode .nfo describes the episode its own number names,
// so the walk matches a block to an episode by number and not by its place in
// the file. A file of two episodes keeps both of them, whatever order the
// blocks come in, and whichever of its episodes a single block describes.
func TestWalkSeriesMatchesEachNFOBlockToItsOwnEpisode(t *testing.T) {
	down := `<episodedetails><title>The Long Way Down</title><season>4</season><episode>10</episode></episodedetails>`
	back := `<episodedetails><title>The Long Way Back</title><season>4</season><episode>11</episode></episodedetails>`
	cases := []struct {
		name   string
		nfo    string
		titles map[string]string
	}{
		{
			name: "blocks in episode order",
			nfo:  down + "\n" + back,
			titles: map[string]string{
				"episode:tvdb:7:s04e10": "The Long Way Down",
				"episode:tvdb:7:s04e11": "The Long Way Back",
			},
		},
		{
			name: "blocks in reverse order",
			nfo:  back + "\n" + down,
			titles: map[string]string{
				"episode:tvdb:7:s04e10": "The Long Way Down",
				"episode:tvdb:7:s04e11": "The Long Way Back",
			},
		},
		{
			name: "one block for the second episode",
			nfo:  back,
			titles: map[string]string{
				"episode:tvdb:7:s04e10": "S04E10",
				"episode:tvdb:7:s04e11": "The Long Way Back",
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			doubleEpisodeVolume(t, root, "Coastline - S04E10-E11.mkv", testCase.nfo)

			result := walkSeries(root, "house/series", nil)
			titles := map[string]string{}
			for _, episode := range result.episodes {
				titles[episode.Id] = episode.Title
			}
			if !reflect.DeepEqual(titles, testCase.titles) {
				t.Errorf("episodes = %v, want %v", titles, testCase.titles)
			}
			if len(result.episodes) != 2 {
				t.Errorf("episode rows = %d, want one row for each episode", len(result.episodes))
			}
		})
	}
}

// An .nfo file whose number the name does not hold is the source for the
// first episode, as it is for a file of one episode, and the rest of the
// range keeps the numbers the name gives it.
func TestWalkSeriesTakesTheFirstNumberFromAnNFOThatDisagreesWithTheName(t *testing.T) {
	root := t.TempDir()
	doubleEpisodeVolume(t, root, "Coastline - S04E10-E11.mkv",
		`<episodedetails><title>The Crossing</title><season>4</season><episode>9</episode></episodedetails>`)

	titles := map[string]string{}
	for _, episode := range walkSeries(root, "house/series", nil).episodes {
		titles[episode.Id] = episode.Title
	}
	want := map[string]string{
		"episode:tvdb:7:s04e09": "The Crossing",
		"episode:tvdb:7:s04e11": "S04E11",
	}
	if !reflect.DeepEqual(titles, want) {
		t.Errorf("episodes = %v, want %v", titles, want)
	}
}

// The parts the walk reads from a file of several episodes: the link of
// each episode and the runtime of each episode.
type episodePart struct {
	Part     int
	Parts    int
	Duration int64
}

// partsOfVideo walks a season folder that holds one video and reads, for
// each episode the walk found, the place its link from that video gives
// it and its runtime.
func partsOfVideo(t *testing.T, root, video string) map[string]episodePart {
	t.Helper()
	result := walkSeries(root, "house/series", nil)
	row := fileRowAt(t, result, filepath.Join("Coastline", "Season 04", video))
	episodes := episodesByID(result)
	parts := map[string]episodePart{}
	for _, item := range row.Items {
		parts[item.Item] = episodePart{Part: item.Part, Parts: item.Parts, Duration: episodes[item.Item].Duration}
	}
	return parts
}

// A file of several episodes splits its probed duration evenly, so each
// episode's link records its place in the file and each episode's runtime
// is its share of the file. A number of the range that names no episode,
// such as E00, still counts in the parts, because its seconds are still in
// the file.
func TestWalkSeriesRecordsEachEpisodesPlaceInItsFile(t *testing.T) {
	cases := []struct {
		name     string
		video    string
		duration float64
		want     map[string]episodePart
	}{
		{
			name: "a single", video: "Coastline - S04E10.mkv", duration: 1320,
			want: map[string]episodePart{
				"episode:tvdb:7:s04e10": {Part: 1, Parts: 1, Duration: 1320},
			},
		},
		{
			name: "a double", video: "Coastline - S04E10-E11.mkv", duration: 2640.5,
			want: map[string]episodePart{
				"episode:tvdb:7:s04e10": {Part: 1, Parts: 2, Duration: 1320},
				"episode:tvdb:7:s04e11": {Part: 2, Parts: 2, Duration: 1320},
			},
		},
		{
			name: "a triple", video: "Coastline - S04E10-E12.mkv", duration: 3960,
			want: map[string]episodePart{
				"episode:tvdb:7:s04e10": {Part: 1, Parts: 3, Duration: 1320},
				"episode:tvdb:7:s04e11": {Part: 2, Parts: 3, Duration: 1320},
				"episode:tvdb:7:s04e12": {Part: 3, Parts: 3, Duration: 1320},
			},
		},
		{
			name: "a first number with no episode", video: "Coastline - S04E00-E01.mkv", duration: 2640,
			want: map[string]episodePart{
				"episode:tvdb:7:s04e01": {Part: 2, Parts: 2, Duration: 1320},
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			season := doubleEpisodeVolume(t, root, testCase.video, "")
			writeProbeLedger(t, season, probedFile{
				Path: testCase.video, Modified: modifiedOf(t, filepath.Join(season, testCase.video)),
				Duration: testCase.duration,
			})

			if got := partsOfVideo(t, root, testCase.video); !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("parts = %+v, want %+v", got, testCase.want)
			}
		})
	}
}

// A file that no probe has read and whose .nfo file gives no stream
// duration has no duration to split, so each episode keeps the runtime its
// own .nfo block gives.
func TestWalkSeriesKeepsTheNFORuntimeOfAFileWithNoDuration(t *testing.T) {
	root := t.TempDir()
	video := "Coastline - S04E10-E11.mkv"
	doubleEpisodeVolume(t, root, video,
		`<episodedetails><season>4</season><episode>10</episode><runtime>25</runtime></episodedetails>`+
			`<episodedetails><season>4</season><episode>11</episode><runtime>20</runtime></episodedetails>`)

	want := map[string]episodePart{
		"episode:tvdb:7:s04e10": {Part: 1, Parts: 2, Duration: 1500},
		"episode:tvdb:7:s04e11": {Part: 2, Parts: 2, Duration: 1200},
	}
	if got := partsOfVideo(t, root, video); !reflect.DeepEqual(got, want) {
		t.Errorf("parts = %+v, want %+v", got, want)
	}
}
