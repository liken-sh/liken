package main

// What a season.nfo <showid> does: the walk reads it, a changed mapping
// reopens the season's facts, and the facts ask the mapped show. The fixture
// is an anthology that TheTVDB keeps as one series and TMDb splits into one
// show per installment.

import (
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

func TestASeasonNFOMapsAProvidersShow(t *testing.T) {
	cases := []struct {
		name        string
		nfo         string
		want        seasonShows
		wantSkipped int
	}{
		{name: "a show and its season",
			nfo:  `<season><showid type="tmdb" season="1">225634</showid></season>`,
			want: seasonShows{"tmdb": {id: "225634", season: 1}}},
		{name: "a show with no season, which keeps the folder's",
			nfo:  `<season><showid type="tvmaze">68626</showid></season>`,
			want: seasonShows{"tvmaze": {id: "68626", season: 2}}},
		{name: "two providers",
			nfo: `<season><seasonnumber>2</seasonnumber><showid type="TMDB" season="1"> 225634 </showid>` +
				`<showid type="tvmaze" season="1">68626</showid></season>`,
			want: seasonShows{"tmdb": {id: "225634", season: 1}, "tvmaze": {id: "68626", season: 1}}},
		{name: "a provider no fact asks by",
			nfo: `<season><showid type="anidb">42</showid></season>`, want: seasonShows{}, wantSkipped: 1},
		{name: "a show with no id",
			nfo: `<season><showid type="tmdb" season="1"></showid></season>`, want: seasonShows{}, wantSkipped: 1},
		{name: "a season that is no number",
			nfo: `<season><showid type="tmdb" season="one">225634</showid></season>`, want: seasonShows{}, wantSkipped: 1},
		{name: "a file that is no XML", nfo: `not xml at all <`, want: seasonShows{}, wantSkipped: 1},
		{name: "a season with no mapping", nfo: `<season><seasonnumber>2</seasonnumber></season>`, want: seasonShows{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, skipped := parseSeasonShows([]byte(test.nfo), 2)

			if got.signature() != test.want.signature() {
				t.Errorf("shows = %q, want %q", got.signature(), test.want.signature())
			}
			if len(skipped) != test.wantSkipped {
				t.Errorf("skipped %v, want %d lines", skipped, test.wantSkipped)
			}
		})
	}
}

// A provider the season maps is asked by the show and the show's season, and
// every other provider is asked as the series.
func TestAMappedSeasonAnswersForItsProviderAlone(t *testing.T) {
	shows := seasonShows{"tmdb": {id: "225634", season: 1}}
	cases := []struct {
		provider   string
		id         string
		wantID     string
		wantSeason int
	}{
		{provider: "tmdb", id: "338676", wantID: "225634", wantSeason: 1},
		{provider: "tvdb", id: "389492", wantID: "389492", wantSeason: 2},
	}
	for _, test := range cases {
		t.Run(test.provider, func(t *testing.T) {
			id, season := shows.on(test.provider, test.id, 2)

			if id != test.wantID || season != test.wantSeason {
				t.Errorf("on = %s season %d, want %s season %d", id, season, test.wantID, test.wantSeason)
			}
		})
	}
}

const mappedSeason = `<season><showid type="tmdb" season="1">113988</showid></season>`

// The signature the fixture's Season 01 takes under mappedSeason.
var mappedSignature = seasonShows{"tmdb": {id: "113988", season: 1}}.signature()

// mapSeason writes the fixture's season.nfo and the two season ledgers as
// facts that asked under the mapping given would have left them. The still
// was written by TMDb at the time the facts ran.
func (e enrichedEpisode) mapSeason(t *testing.T, nfo, askedUnder string) {
	t.Helper()
	if nfo != "" {
		writeFile(t, filepath.Join(e.season, seasonNFOName), nfo)
	}
	writeFactLedger(t, e.season, factEpisodeThumb, likenLedger{
		Items: []likenItem{{Path: replacedEntry, Provider: providerNames{providerBlockTMDb},
			Written: e.made, Show: askedUnder}},
		Attempts: []likenAttempt{{Path: replacedEntry, At: e.made, Result: attemptFound, Show: askedUnder}},
	})
	writeFactLedger(t, e.season, factMarks, likenLedger{
		Marks: []markEntry{{Path: replacedEntry, Kind: markKindIntro, End: milliseconds(50000), Source: providerBlockTheIntroDB}},
		Attempts: []likenAttempt{{Path: replacedEntry, At: e.made, Result: attemptFound,
			Provider: providerNames{providerBlockTheIntroDB}, Show: askedUnder}},
	})
}

// A season whose mapping is not the one its facts asked under opens those
// facts again, and its still and its spans leave the rows until the facts
// replace them.
func TestAChangedMappingReopensTheSeason(t *testing.T) {
	cases := []struct {
		name       string
		nfo        string
		askedUnder string
		wantGaps   map[string]int
	}{
		{name: "a season with no mapping, asked as the series",
			wantGaps: map[string]int{factProbe: 0, factTrickplay: 0, factEpisodeThumb: 0, factMarks: 0}},
		{name: "a mapping the facts asked under", nfo: mappedSeason, askedUnder: mappedSignature,
			wantGaps: map[string]int{factProbe: 0, factTrickplay: 0, factEpisodeThumb: 0, factMarks: 0}},
		{name: "a mapping added after the facts asked", nfo: mappedSeason,
			wantGaps: map[string]int{factProbe: 0, factTrickplay: 0, factEpisodeThumb: 1, factMarks: 1}},
		{name: "a mapping removed after the facts asked under it", askedUnder: mappedSignature,
			wantGaps: map[string]int{factProbe: 0, factTrickplay: 0, factEpisodeThumb: 1, factMarks: 1}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			episode := seedEnrichedEpisode(t)
			episode.mapSeason(t, test.nfo, test.askedUnder)
			catalog, _ := newSQLiteCatalog(t)

			episode.rescan(t, catalog)

			if got := gapsOf(t, catalog); !mapsEqual(got, test.wantGaps) {
				t.Errorf("gaps = %v, want %v", got, test.wantGaps)
			}
		})
	}
}

func TestAStillOfAnotherShowLeavesTheEpisode(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	episode.mapSeason(t, mappedSeason, "")

	result := readFolder(episode.scan(), episode.series)

	if items := linkedTo(result, filepath.Join(replacedSeries, "Season 01", replacedThumb)); items != nil {
		t.Errorf("the thumbnail links to %v, want no episode for a still of another show", items)
	}
	for _, mark := range result.marks {
		if mark.Path == episode.path {
			t.Errorf("mark = %+v, want no span asked of another show", mark)
		}
	}
	if result.reopened.reshown != 1 {
		t.Errorf("reshown = %d, want the one still", result.reopened.reshown)
	}
}

// A season art file that TMDb wrote for another show leaves the rows, so the
// season's gap opens.
func TestASeasonPosterOfAnotherShowOpensItsGap(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	poster := filepath.Join(episode.series, "season01-poster.jpg")
	writeFile(t, poster, "a poster of another show")
	setModified(t, poster, episode.made)
	writeFactLedger(t, episode.series, factSeasonPoster, likenLedger{
		Items: []likenItem{{Path: "season01-poster.jpg", Provider: providerNames{providerBlockTMDb},
			Written: episode.made}},
		Attempts: []likenAttempt{{Path: "season01-poster.jpg", At: episode.made, Result: attemptFound}},
	})
	writeFile(t, filepath.Join(episode.season, seasonNFOName), mappedSeason)

	result := readFolder(episode.scan(), episode.series)

	if row := fileRowOrNil(result, filepath.Join(replacedSeries, "season01-poster.jpg")); row != nil {
		t.Errorf("the poster row = %+v, want none for a poster of another show", row)
	}
	if got := liftedFacts(result, filepath.Join(replacedSeries, "season01-poster.jpg")); len(got) != 0 {
		t.Errorf("lifted %v, want no attempt asked of another show", got)
	}
}

func fileRowOrNil(result *walkResult, path string) *fileRow {
	for i := range result.files {
		if result.files[i].Path == path {
			return &result.files[i]
		}
	}
	return nil
}

// The walk names each <showid> it skipped, so a person reads why a mapping
// did nothing.
func TestTheWalkNamesAShowIDItSkipped(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	writeFile(t, filepath.Join(episode.season, seasonNFOName), `<season><showid type="anidb">42</showid></season>`)

	result := readFolder(episode.scan(), episode.series)

	if len(result.reopened.skippedShows) != 1 {
		t.Errorf("notes = %v, want one line for the skipped showid", result.reopened.skippedShows)
	}
}

// The still of a mapped season is the show's, asked by the show's id and its
// season, and the attempt records the mapping it asked under.
func TestTheArtPhaseAsksTheMappedShow(t *testing.T) {
	cases := []struct {
		name     string
		fact     string
		gap      func(e enrichedEpisode) artGap
		endpoint string
		list     string
		target   func(e enrichedEpisode) string
		ledger   func(e enrichedEpisode) string
	}{
		{name: "an episode's still", fact: factEpisodeThumb, list: tmdbStills,
			gap: func(e enrichedEpisode) artGap {
				return artGap{key: e.path, tmdb: "2001", season: 1, episode: 2}
			},
			endpoint: "/3/tv/113988/season/1/episode/2/images",
			target:   func(e enrichedEpisode) string { return e.thumb },
			ledger:   func(e enrichedEpisode) string { return e.season }},
		{name: "a season's poster", fact: factSeasonPoster, list: tmdbPosters,
			gap: func(e enrichedEpisode) artGap {
				return artGap{key: filepath.Join(replacedSeries, "season01-poster.jpg"), tmdb: "2001", season: 1}
			},
			endpoint: "/3/tv/113988/season/1/images",
			target:   func(e enrichedEpisode) string { return filepath.Join(e.series, "season01-poster.jpg") },
			ledger:   func(e enrichedEpisode) string { return e.series }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				episode := seedEnrichedEpisode(t)
				episode.mapSeason(t, mappedSeason, "")
				gap := test.gap(episode)
				client, fake := newArtTMDb(t, map[string]string{
					tmdbKey(test.endpoint, "", ""):          imagesAnswer(test.list, "/mapped.jpg", artLanguage),
					tmdbKey("/t/p/w300/mapped.jpg", "", ""): testImage,
					tmdbKey("/t/p/w780/mapped.jpg", "", ""): testImage,
				})
				work, _ := testEnricher(t, libraryKindSeries, episode.root, nil)

				work.artOne(t.Context(), tmdbArtLine(client), artTypes[test.fact], gap)

				if fake.served[tmdbKey(test.endpoint, "", "")] != 1 {
					t.Errorf("served %v, want one ask of %s", fake.served, test.endpoint)
				}
				if got := readFileString(t, test.target(episode)); got != testImage {
					t.Errorf("the file holds %q, want the mapped show's image", got)
				}
				ledger := artLedger(t, test.ledger(episode), test.fact)
				entry := gap.entry()
				if attempt := attemptAt(ledger, entry); attempt.Show != mappedSignature {
					t.Errorf("the attempt asked under %q, want %q", attempt.Show, mappedSignature)
				}
			})
		})
	}
}

func attemptAt(ledger likenLedger, path string) likenAttempt {
	for _, attempt := range ledger.Attempts {
		if attempt.Path == path {
			return attempt
		}
	}
	return likenAttempt{}
}

// A still that this fact wrote under another mapping is replaced, and one a
// person or another tool changed since is kept.
func TestTheArtPhaseReplacesAStillOfAnotherShow(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		modified func(e enrichedEpisode) time.Time
		want     string
	}{
		{name: "a still TMDb wrote under no mapping", provider: providerBlockTMDb,
			modified: func(e enrichedEpisode) time.Time { return e.made }, want: testImage},
		{name: "a still changed after TMDb wrote it", provider: providerBlockTMDb,
			modified: func(e enrichedEpisode) time.Time { return e.made.Add(time.Hour) },
			want:     "a still of the first encode"},
		{name: "a still another tool wrote", provider: artProviderExisting,
			modified: func(e enrichedEpisode) time.Time { return e.made }, want: "a still of the first encode"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				episode := seedEnrichedEpisode(t)
				episode.mapSeason(t, mappedSeason, "")
				writeFactLedger(t, episode.season, factEpisodeThumb, likenLedger{
					Items: []likenItem{{Path: replacedEntry, Provider: providerNames{test.provider},
						Written: episode.made}},
				})
				setModified(t, episode.thumb, test.modified(episode))
				client, _ := newArtTMDb(t, map[string]string{
					tmdbKey("/3/tv/113988/season/1/episode/2/images", "", ""): imagesAnswer(tmdbStills, "/mapped.jpg", artLanguage),
					tmdbKey("/t/p/w300/mapped.jpg", "", ""):                   testImage,
				})
				work, _ := testEnricher(t, libraryKindSeries, episode.root, nil)

				work.artOne(t.Context(), tmdbArtLine(client), artTypes[factEpisodeThumb],
					artGap{key: episode.path, tmdb: "2001", season: 1, episode: 2})

				if got := readFileString(t, episode.thumb); got != test.want {
					t.Errorf("the thumbnail holds %q, want %q", got, test.want)
				}
			})
		})
	}
}

// Fanart.tv's season art reads the season of the TheTVDB show the season
// maps, so the folder's Season 02 takes the mapped show's season 1 poster.
func TestFanartSeasonArtReadsTheMappedSeason(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		folder := "Anthology (2022)"
		catalog, _ := newSQLiteCatalog(t)
		root := t.TempDir()
		writeFile(t, filepath.Join(root, folder, "Season 02", "Anthology - S02E05.mkv"), "video")
		writeFile(t, filepath.Join(root, folder, "Season 02", seasonNFOName),
			`<season><showid type="tvdb" season="1">800002</showid></season>`)
		writeSeriesNFO(t, root, folder, "800001")
		seedArtSeries(t, catalog, folder, []int{2})
		work, _ := testEnricher(t, libraryKindSeries, root, catalog)
		answers := map[string]string{}
		client, _ := newArtFanart(t, answers)
		answers[fanartSeriesPath+"800002"] = fanartSeriesArt(client.base)

		if err := work.artGap(t.Context(), factSeasonPoster, artLineOf(fanartArtAnswerer{client: client})); err != nil {
			t.Fatal(err)
		}

		got := readFileString(t, filepath.Join(root, folder, "season02-poster.jpg"))
		if got != "/art/seasonposter-1.jpg" {
			t.Errorf("season02-poster.jpg holds %q, want the mapped show's season 1 poster", got)
		}
	})
}
