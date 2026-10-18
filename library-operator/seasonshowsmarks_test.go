package main

// What the marks fact does with a season that a <showid> maps: each provider
// is asked by the id and the season that go together, the attempt records the
// mapping, and the spans asked of another show leave with it.

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
)

// Season 2 of the anthology is, on TMDb, season 1 of its own show.
var anthologySeason = seasonShows{"tmdb": {id: "225634", season: 1}}

func TestTheIntroDBAsksTheIDAndTheSeasonThatGoTogether(t *testing.T) {
	cases := []struct {
		name  string
		ids   providerIDs
		shows seasonShows
		want  string
	}{
		{name: "a TheTVDB id the season does not map",
			ids: providerIDs{"tvdb": "389492", "tmdb": "338676"}, shows: anthologySeason,
			want: "tvdb_id=389492 season=2"},
		{name: "a TMDb id the season maps",
			ids: providerIDs{"tmdb": "338676"}, shows: anthologySeason,
			want: "tmdb_id=225634 season=1"},
		{name: "a TheTVDB id the season maps",
			ids: providerIDs{"tvdb": "389492"}, shows: seasonShows{"tvdb": {id: "800002", season: 1}},
			want: "tvdb_id=800002 season=1"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, fake := newFakeTheIntroDB(t, "", http.StatusOK, `{"tvdb_id":1,"type":"tv"}`)

				if _, err := newTheIntroDBMarkAnswerer(client).marks(t.Context(),
					markFile{ids: test.ids, shows: test.shows, season: 2, episode: 3}); err != nil {
					t.Fatal(err)
				}
				query := fake.requests[0].URL.Query()
				named := []string{}
				for _, name := range []string{"tvdb_id", "tmdb_id", "imdb_id", "season"} {
					if query.Has(name) {
						named = append(named, name+"="+query.Get(name))
					}
				}
				if got := strings.Join(named, " "); got != test.want {
					t.Errorf("the ask named %q, want %q", got, test.want)
				}
			})
		})
	}
}

func TestIntroDBAsksTheMappedIMDbShow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, fake := newFakeIntroDB(t, http.StatusNotFound, `{}`)

		if _, err := newIntroDBMarkAnswerer(client).marks(t.Context(), markFile{
			ids: providerIDs{"imdb": "tt13207736"}, season: 2, episode: 3,
			shows: seasonShows{"imdb": {id: "tt9000001", season: 1}},
		}); err != nil {
			t.Fatal(err)
		}

		query := fake.requests[0].URL.Query()
		if query.Get("imdb_id") != "tt9000001" || query.Get("season") != "1" {
			t.Errorf("the ask named %v, want the mapped show and its season", query)
		}
	})
}

// The answerer a test reads the asked file from.
type askedMarks struct {
	asked *markFile
}

func (a askedMarks) providerBlock() string { return providerBlockTheIntroDB }

func (a askedMarks) marks(_ context.Context, file markFile) ([]markEntry, error) {
	*a.asked = file
	return []markEntry{answeredSpan(providerBlockTheIntroDB, markKindIntro, 0, 61000)}, nil
}

// The marks phase reads the mapping beside the file before it asks, records
// the mapping on the attempt, and drops the spans that an ask of another show
// left, from the sources that did not answer as well.
func TestTheMarksPhaseAsksUnderTheSeasonsMapping(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		episode := seedEnrichedEpisode(t)
		episode.mapSeason(t, mappedSeason, "")
		writeFactLedger(t, episode.season, factMarks, likenLedger{
			Marks: []markEntry{
				{Path: replacedEntry, Kind: markKindCredits, Start: milliseconds(1400000), Source: providerBlockIntroDB},
			},
			Attempts: []likenAttempt{{Path: replacedEntry, At: episode.made, Result: attemptFound}},
		})
		var asked markFile
		work, _ := testEnricher(t, libraryKindSeries, episode.root, nil)

		work.marksOne(t.Context(), markLineOf(askedMarks{asked: &asked}), episode.path,
			markFile{episodes: 1, ids: providerIDs{"tmdb": "2001"}, season: 1, episode: 2})

		if asked.shows.signature() != mappedSignature {
			t.Errorf("the ask carried %q, want %q", asked.shows.signature(), mappedSignature)
		}
		ledger := artLedger(t, filepath.Join(episode.season), factMarks)
		if got := markText(ledger.Marks); strings.Join(got, "|") != replacedEntry+" intro 0s-1m1s theintrodb" {
			t.Errorf("the ledger holds %v, want the new answer alone", got)
		}
		if attempt := attemptAt(ledger, replacedEntry); attempt.Show != mappedSignature {
			t.Errorf("the attempt asked under %q, want %q", attempt.Show, mappedSignature)
		}
	})
}
