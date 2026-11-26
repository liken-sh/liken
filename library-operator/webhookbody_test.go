package main

// The tests of the document a walk posts: the rows of a movie with the rows
// that belong to it, the JSON columns as JSON, and a series with the episodes
// whose videos changed.

import (
	"encoding/json"
	"slices"
	"testing"
	"testing/synctest"
)

// A movie's genres, its credits with the people they name, and its body
// reach the document, and a JSON column is an object and not a string.
func TestATitleCarriesItsRowsAndTheRowsThatBelongToIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		if _, err := catalog.UpsertMovies(t.Context(), []movieRow{{
			Id: "movie:tmdb:1002", Library: "house/movies", Kind: libraryKindMovies,
			Path: "Another Survey (2001)", Title: "Another Survey", Released: "2001",
			Arts: []string{"Another Survey (2001)/poster.jpg"}, Body: movieBody{Plot: "A survey, again."},
		}}); err != nil {
			t.Fatal(err)
		}
		seedVideo(t, catalog, "Another Survey (2001)/movie.mkv", 300, "movie:tmdb:1002")
		if _, err := catalog.UpsertGenres(t.Context(), genreRows("house/movies", "movie:tmdb:1002",
			[]string{"Drama", "Mystery"})); err != nil {
			t.Fatal(err)
		}
		if _, err := catalog.UpsertContributors(t.Context(), []contributorRow{{
			Library: "house/movies", Path: ".contributors/Ada Example", Name: "Ada Example", Born: "1970",
		}}); err != nil {
			t.Fatal(err)
		}
		if _, err := catalog.UpsertCredits(t.Context(), []creditRow{
			{Library: "house/movies", Item: "movie:tmdb:1002", Billing: 0,
				Contributor: ".contributors/Ada Example", Name: "Ada Example", Part: "actor", Role: "The Surveyor"},
			{Library: "house/movies", Item: "movie:tmdb:1002", Billing: 1, Name: "Bo Nobody", Part: "director"},
		}); err != nil {
			t.Fatal(err)
		}

		titles, err := walkTitles(t.Context(), catalog, "house/movies", libraryKindMovies,
			map[string]string{"Another Survey (2001)/movie.mkv": videoAdded})
		if err != nil {
			t.Fatal(err)
		}

		if len(titles) != 1 {
			t.Fatalf("titles = %d, want the one movie", len(titles))
		}
		title := titles[0]
		if plot := title.Row["body"].(map[string]any)["plot"]; plot != "A survey, again." {
			t.Errorf("body.plot = %v, want the plot as JSON", plot)
		}
		if arts := title.Row["arts"].([]any); len(arts) != 1 {
			t.Errorf("arts = %v, want the one poster as a JSON list", arts)
		}
		var genres []string
		for _, genre := range title.Genres {
			genres = append(genres, genre["genre"].(string))
		}
		if !slices.Equal(genres, []string{"Drama", "Mystery"}) {
			t.Errorf("genres = %v, want both in rank order", genres)
		}
		if len(title.Credits) != 2 || title.Credits[0].Contributor["born"] != "1970" || title.Credits[1].Contributor != nil {
			t.Errorf("credits = %+v, want the actor with the row of her person and the director with none", title.Credits)
		}
		if title.Files[0].Row["video_codec"] != "hevc" {
			t.Errorf("file = %v, want the video's row", title.Files[0].Row)
		}
	})
}

// A series is one title, with the episodes whose videos changed and not its
// other episodes, and it is changed when it already had a video.
func TestASeriesCarriesTheEpisodesWhoseVideosChanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		if _, err := catalog.UpsertSeries(t.Context(), []seriesRow{{
			Id: "series:tvdb:2001", Library: "house/movies", Kind: libraryKindSeries,
			Path: "The Long Road", Title: "The Long Road",
		}}); err != nil {
			t.Fatal(err)
		}
		var episodes []episodeRow
		for number := 1; number <= 3; number++ {
			episodes = append(episodes, episodeRow{
				Id: "episode:tvdb:" + string(rune('0'+number)), Library: "house/movies", Kind: libraryKindSeries,
				Series: "series:tvdb:2001", Season: 1, Episode: number,
			})
		}
		if _, err := catalog.UpsertEpisodes(t.Context(), episodes); err != nil {
			t.Fatal(err)
		}
		for _, episode := range episodes {
			seedVideo(t, catalog, "The Long Road/S01E0"+episode.Id[len(episode.Id)-1:]+".mkv", 100, episode.Id)
		}

		titles, err := walkTitles(t.Context(), catalog, "house/movies", libraryKindSeries, map[string]string{
			"The Long Road/S01E03.mkv": videoAdded,
			"The Long Road/S01E02.mkv": videoReplaced,
		})
		if err != nil {
			t.Fatal(err)
		}

		if len(titles) != 1 || titles[0].Table != "series" || titles[0].Change != titleChanged {
			t.Fatalf("titles = %+v, want the one series, changed", titles)
		}
		var got []string
		for _, episode := range titles[0].Episodes {
			got = append(got, episode.Row["id"].(string)+" "+episode.Files[0].Change)
		}
		if want := []string{"episode:tvdb:2 replaced", "episode:tvdb:3 added"}; !slices.Equal(got, want) {
			t.Errorf("episodes = %v, want %v", got, want)
		}
		encoded, _ := json.Marshal(titles[0])
		var shape map[string]any
		_ = json.Unmarshal(encoded, &shape)
		if _, held := shape["files"]; held {
			t.Error("a series carries its files under its episodes, not beside them")
		}
	})
}
