package main

// What these tests read: how the episode .nfo beside a double-episode file
// and the range in its name together number the episodes the file holds.

import (
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
