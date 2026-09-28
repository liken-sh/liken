package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAnEpisodeFileNameCarriesItsEpisodeTitle(t *testing.T) {
	cases := []struct {
		name        string
		file        string
		wantSeason  int
		wantEpisode int
		wantTitle   string
		wantHeld    bool
	}{
		{
			name:        "the plain form",
			file:        "Pine Hollow - S01E02 - Lanterns at Dusk.mkv",
			wantSeason:  1,
			wantEpisode: 2,
			wantTitle:   "Lanterns at Dusk",
			wantHeld:    true,
		},
		{
			name:        "a quality tag after the title",
			file:        "Pine Hollow - S01E02 - Lanterns at Dusk [1080p].mkv",
			wantSeason:  1,
			wantEpisode: 2,
			wantTitle:   "Lanterns at Dusk",
			wantHeld:    true,
		},
		{
			name:        "more segments after the title",
			file:        "Pine Hollow - S01E02 - Lanterns at Dusk - 1080p - x264.mkv",
			wantSeason:  1,
			wantEpisode: 2,
			wantTitle:   "Lanterns at Dusk",
			wantHeld:    true,
		},
		{
			name:        "a 1x02 marker",
			file:        "Pine Hollow 1x02 Lanterns at Dusk.mkv",
			wantSeason:  1,
			wantEpisode: 2,
			wantTitle:   "Lanterns at Dusk",
			wantHeld:    true,
		},
		{
			name:        "a double episode marker",
			file:        "Pine Hollow - S01E01E02 - Pilot.mkv",
			wantSeason:  1,
			wantEpisode: 1,
			wantTitle:   "Pilot",
			wantHeld:    true,
		},
		{name: "no marker at all", file: "Pine Hollow.mkv"},
		{name: "nothing after the marker", file: "Pine Hollow - S01E02.mkv"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			clue, held := episodeClueFrom(test.file)

			if held != test.wantHeld {
				t.Fatalf("held = %t, want %t", held, test.wantHeld)
			}
			want := episodeClue{season: test.wantSeason, episode: test.wantEpisode, title: test.wantTitle}
			if held && clue != want {
				t.Errorf("clue = %+v, want %+v", clue, want)
			}
		})
	}
}

// One TMDb series search result, as the fake server lists it.
func tmdbSeriesJSON(id int, name, date string) string {
	return `{"id":` + strconv.Itoa(id) + `,"name":"` + name + `","original_name":"` + name +
		`","first_air_date":"` + date + `"}`
}

var ruckusResults = `{"results":[` +
	tmdbSeriesJSON(2101, "Ruckus", "2004-01-17") + `,` +
	tmdbSeriesJSON(2102, "Ruckus", "2011-01-13") + `]}`

func TestTheEpisodeRungPartsTwoSeriesOfOneName(t *testing.T) {
	cases := []struct {
		name           string
		seasons        map[string]string
		episodes       []episodeClue
		wantID         int
		wantCandidates int
	}{
		{
			name: "the episode names name one series",
			seasons: map[string]string{
				tmdbKey("/3/tv/2101/season/1", "", ""): `{"episodes":[{"episode_number":1,"name":"Episode One"},{"episode_number":2,"name":"Episode Two"}]}`,
				tmdbKey("/3/tv/2102/season/1", "", ""): `{"episodes":[{"episode_number":1,"name":"Pilot"},{"episode_number":2,"name":"Pete at Sea"}]}`,
			},
			episodes: []episodeClue{
				{season: 1, episode: 1, title: "Pilot"},
				{season: 1, episode: 2, title: "Pete at Sea"},
			},
			wantID: 2102,
		},
		{
			name: "no series carries the episode names",
			seasons: map[string]string{
				tmdbKey("/3/tv/2101/season/1", "", ""): `{"episodes":[{"episode_number":1,"name":"Episode One"},{"episode_number":2,"name":"Episode Two"}]}`,
				tmdbKey("/3/tv/2102/season/1", "", ""): `{"episodes":[{"episode_number":1,"name":"Pilot"},{"episode_number":2,"name":"Pete at Sea"}]}`,
			},
			episodes: []episodeClue{
				{season: 1, episode: 1, title: "A Name Nobody Wrote"},
				{season: 1, episode: 2, title: "Nor This One"},
			},
			wantCandidates: 2,
		},
		{
			name: "both series carry the episode names",
			seasons: map[string]string{
				tmdbKey("/3/tv/2101/season/1", "", ""): `{"episodes":[{"episode_number":1,"name":"Pilot"},{"episode_number":2,"name":"Pete at Sea"}]}`,
				tmdbKey("/3/tv/2102/season/1", "", ""): `{"episodes":[{"episode_number":1,"name":"Pilot"},{"episode_number":2,"name":"Pete at Sea"}]}`,
			},
			episodes: []episodeClue{
				{season: 1, episode: 1, title: "Pilot"},
				{season: 1, episode: 2, title: "Pete at Sea"},
			},
			wantCandidates: 2,
		},
		{
			name: "one episode name of two matches",
			seasons: map[string]string{
				tmdbKey("/3/tv/2101/season/1", "", ""): `{"episodes":[{"episode_number":1,"name":"Episode One"},{"episode_number":2,"name":"Episode Two"}]}`,
				tmdbKey("/3/tv/2102/season/1", "", ""): `{"episodes":[{"episode_number":1,"name":"Pilot"},{"episode_number":2,"name":"Pete at Sea"}]}`,
			},
			episodes: []episodeClue{
				{season: 1, episode: 1, title: "Pilot"},
				{season: 1, episode: 2, title: "A Name Nobody Wrote"},
			},
			wantCandidates: 2,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			answers := map[string]string{tmdbKey("/3/search/tv", "Ruckus", ""): ruckusResults}
			for key, answer := range test.seasons {
				answers[key] = answer
			}
			client, _ := newFakeTMDb(t, answers)

			answer, err := climbIdentityLadder(t.Context(), client, identitySearch{
				kind: libraryKindSeries, title: "Ruckus", episodes: test.episodes,
			})
			if err != nil {
				t.Fatal(err)
			}
			if answer.id != test.wantID {
				t.Errorf("id = %d, want %d", answer.id, test.wantID)
			}
			if len(answer.candidates) != test.wantCandidates {
				t.Errorf("candidates = %+v, want %d", answer.candidates, test.wantCandidates)
			}
			if test.wantID > 0 && answer.reason != reasonFrom(testTitle, testEpisodes) {
				t.Errorf("reason = %q, want the episode names", answer.reason)
			}
		})
	}
}

func TestAMovieNeverAsksForASeason(t *testing.T) {
	client, fake := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "Quiet Moon", ""): `{"results":[` +
			tmdbResultJSON(1121, "Quiet Moon", "1972-03-24") + `,` +
			tmdbResultJSON(1122, "Quiet Moon", "2002-11-03") + `]}`,
	})

	answer, err := climbIdentityLadder(t.Context(), client, identitySearch{
		kind:     libraryKindMovies,
		title:    "Quiet Moon",
		episodes: []episodeClue{{season: 1, episode: 1, title: "Pilot"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(answer.candidates) != 2 {
		t.Fatalf("candidates = %+v, want both", answer.candidates)
	}
	for _, path := range fake.requestPath {
		if strings.Contains(path, "/season/") {
			t.Errorf("the ladder asked %q, want no season read for a movie", path)
		}
	}
}

func TestTheEpisodeRungFailsWhereTheProviderFails(t *testing.T) {
	client, fake := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/tv", "Ruckus", ""): ruckusResults,
	})
	fake.statuses[tmdbKey("/3/tv/2101/season/1", "", "")] = 500

	_, err := climbIdentityLadder(t.Context(), client, identitySearch{
		kind:  libraryKindSeries,
		title: "Ruckus",
		episodes: []episodeClue{
			{season: 1, episode: 1, title: "Pilot"},
			{season: 1, episode: 2, title: "Pete at Sea"},
		},
	})

	if err == nil {
		t.Error("the ladder reported no error, want the provider's")
	}
}

func TestTheCluesComeOffTheFirstSeasonInTheFolder(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "Pine Hollow")
	writeFile(t, filepath.Join(folder, "Season 02", "Pine Hollow - S02E01 - The Long Night Road.mkv"), "video")
	writeFile(t, filepath.Join(folder, "Season 01", "Pine Hollow - S01E02 - Lanterns at Dusk.mkv"), "video")
	writeFile(t, filepath.Join(folder, "Season 01", "Pine Hollow - S01E01 - Pilot.mkv"), "video")
	writeFile(t, filepath.Join(folder, "Season 01", "Pine Hollow - S01E03.mkv"), "video")
	work, _ := testEnricher(t, libraryKindSeries, root, nil)

	clues := work.episodeClues(folder)

	want := []episodeClue{
		{season: 1, episode: 1, title: "Pilot"},
		{season: 1, episode: 2, title: "Lanterns at Dusk"},
	}
	if len(clues) != len(want) {
		t.Fatalf("clues = %+v, want %+v", clues, want)
	}
	for at, clue := range clues {
		if clue != want[at] {
			t.Errorf("clue %d = %+v, want %+v", at, clue, want[at])
		}
	}
}

// A season folder of count episode files, each named with its episode title.
func writeSeasonOne(t *testing.T, folder string, count int) {
	t.Helper()
	for episode := 1; episode <= count; episode++ {
		name := fmt.Sprintf("Pine Hollow - S01E%02d - Episode %d.mkv", episode, episode)
		writeFile(t, filepath.Join(folder, "Season 01", name), "video")
	}
}

func TestAFolderHandsTheRungTwelveCluesAtTheMost(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "Pine Hollow")
	writeSeasonOne(t, folder, 15)
	work, _ := testEnricher(t, libraryKindSeries, root, nil)

	clues := work.episodeClues(folder)

	if len(clues) != maxEpisodeClues {
		t.Fatalf("clues = %d, want %d", len(clues), maxEpisodeClues)
	}
	if clues[0].episode != 1 || clues[maxEpisodeClues-1].episode != maxEpisodeClues {
		t.Errorf("clues run %d to %d, want 1 to %d", clues[0].episode,
			clues[maxEpisodeClues-1].episode, maxEpisodeClues)
	}
}

func TestAMovieFolderReadsNoEpisodeClues(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Long Survey (1982)")
	writeFile(t, filepath.Join(folder, "The Long Survey - S01E01 - Pilot.mkv"), "video")
	work, _ := testEnricher(t, libraryKindMovies, root, nil)

	if clues := work.episodeClues(folder); len(clues) != 0 {
		t.Errorf("clues = %+v, want none for a movie", clues)
	}
}

func TestABareSeriesFolderIdentifiesByItsEpisodeNames(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "Pine Hollow"
	writeFile(t, filepath.Join(root, folder, "Season 01", "Pine Hollow - S01E01 - Pilot.mkv"), "video")
	writeFile(t, filepath.Join(root, folder, "Season 01", "Pine Hollow - S01E02 - Lanterns at Dusk.mkv"), "video")
	seedIdentityGap(t, catalog, libraryKindSeries, folder, "", 0)
	work, _ := testEnricher(t, libraryKindSeries, root, catalog)
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/tv", "Pine Hollow", ""): `{"results":[` +
			tmdbSeriesJSON(2103, "Pine Hollow", "1990-04-12") + `,` +
			tmdbSeriesJSON(9999, "Pine Hollow", "2017-05-25") + `]}`,
		tmdbKey("/3/tv/2103/season/1", "", ""): `{"episodes":[{"episode_number":1,"name":"Pilot"},{"episode_number":2,"name":"Lanterns at Dusk"}]}`,
		tmdbKey("/3/tv/9999/season/1", "", ""): `{"episodes":[{"episode_number":1,"name":"The Revisit, Part 1"},{"episode_number":2,"name":"The Revisit, Part 2"}]}`,
	})

	if err := work.identityGap(t.Context(), client); err != nil {
		t.Fatal(err)
	}

	nfo := readFileString(t, filepath.Join(root, folder, seriesNFOName))
	if !strings.Contains(nfo, `<uniqueid type="tmdb" default="true">2103</uniqueid>`) {
		t.Errorf("the .nfo file holds no id:\n%s", nfo)
	}
	ledger, err := readLikenLedger(filepath.Join(root, folder), factIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Items) != 1 || ledger.Items[0].Reason != reasonFrom(testTitle, testEpisodes) {
		t.Errorf("ledger items = %+v, want the reason that names the episodes", ledger.Items)
	}
}

func TestSpecialsAreTheFirstSeasonOnlyWhenAlone(t *testing.T) {
	cases := []struct {
		name string
		in   []episodeClue
		want int
	}{
		{"season one before the specials", []episodeClue{{season: 0, episode: 1, title: "a"}, {season: 1, episode: 1, title: "b"}}, 1},
		{"specials alone", []episodeClue{{season: 0, episode: 2, title: "a"}, {season: 0, episode: 1, title: "b"}}, 0},
		{"season two before three", []episodeClue{{season: 3, episode: 1, title: "a"}, {season: 2, episode: 1, title: "b"}}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, clue := range firstSeasonClues(c.in) {
				if clue.season != c.want {
					t.Fatalf("kept season %d, want %d", clue.season, c.want)
				}
			}
		})
	}
}
