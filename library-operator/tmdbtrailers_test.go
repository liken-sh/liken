package main

// What these tests read: the videos call of each kind, the trailer every
// field of a TMDb video becomes, the videos the answerer records none of,
// and the title with no TMDb id.

import (
	"reflect"
	"testing"
)

// A movie and a series make the same call on their own path.
func TestTheTMDbVideosCallAsksTheTitlesOwnPath(t *testing.T) {
	answer := trailerFixture(t, "tmdb-videos-movie.json")
	cases := []struct {
		name string
		kind string
		path string
	}{
		{name: "a movie", kind: libraryKindMovies, path: "/3/movie/603/videos"},
		{name: "a series", kind: libraryKindSeries, path: "/3/tv/603/videos"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, fake := newFakeTMDb(t, map[string]string{tmdbKey(test.path, "", ""): answer})

			videos, err := client.videos(t.Context(), test.kind, 603)
			if err != nil {
				t.Fatal(err)
			}

			if len(videos) != 6 {
				t.Errorf("the call read %d videos, want 6", len(videos))
			}
			if got := fake.requestPath; len(got) != 1 || got[0] != tmdbKey(test.path, "", "") {
				t.Errorf("the call asked %v, want %s", got, test.path)
			}
		})
	}
}

// The answerer that reads one movie's videos.
func newTrailerTMDb(t *testing.T) tmdbTrailerAnswerer {
	t.Helper()
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/movie/603/videos", "", ""): trailerFixture(t, "tmdb-videos-movie.json"),
	})
	return newTMDbTrailerAnswerer(client)
}

// Every field of a TMDb video reaches the trailer, the size as the
// resolution. A site the answerer has no address for is dropped.
// A clip and a video of another kind are dropped as well, because a
// featurette is no trailer of the title.
func TestTheTMDbTrailerAnswererReadsEveryVideo(t *testing.T) {
	answerer := newTrailerTMDb(t)

	entries, err := answerer.trailers(t.Context(), trailerTitle{kind: libraryKindMovies,
		ids: providerIDs{"tmdb": "603"}, languages: []string{"en"}})
	if err != nil {
		t.Fatal(err)
	}

	want := []trailerEntry{
		{Path: likenSelfPath, Provider: providerBlockTMDb, Key: "vKQi3bBA1y8",
			Site: trailerSiteYouTube, URL: "https://www.youtube.com/watch?v=vKQi3bBA1y8",
			Name: "Official Trailer", Kind: trailerKindTrailer, Language: "en", Official: true,
			Published: "2026-03-18", Resolution: 1080,
			Score: 100, Reason: "keyed by the tmdb id; trailer; en"},
		{Path: likenSelfPath, Provider: providerBlockTMDb, Key: "m8e-FF8MsqU",
			Site: trailerSiteYouTube, URL: "https://www.youtube.com/watch?v=m8e-FF8MsqU",
			Name: "Teaser", Kind: trailerKindTeaser, Language: "en",
			Published: "2025-11-02", Resolution: 720,
			Score: 80, Reason: "keyed by the tmdb id; teaser; en; unofficial"},
		{Path: likenSelfPath, Provider: providerBlockTMDb, Key: "76979871",
			Site: trailerSiteVimeo, URL: "https://vimeo.com/76979871",
			Name: "Bande-annonce", Kind: trailerKindTrailer, Language: "fr", Official: true,
			Published: "2026-04-01", Resolution: 1080,
			Score: 70, Reason: "keyed by the tmdb id; trailer; language fr not preferred"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("the answerer held\n%+v\nwant\n%+v", entries, want)
	}
	if got := answerer.providerBlock(); got != providerBlockTMDb {
		t.Errorf("the answerer names the block %q, want %q", got, providerBlockTMDb)
	}
}

// A title with no TMDb id is no trailer and no call.
func TestTheTMDbTrailerAnswererHoldsNothingWithoutAnID(t *testing.T) {
	client, fake := newFakeTMDb(t, map[string]string{})
	answerer := newTMDbTrailerAnswerer(client)

	entries, err := answerer.trailers(t.Context(),
		trailerTitle{kind: libraryKindMovies, ids: providerIDs{"imdb": "tt0133093"}})

	if err != nil || len(entries) != 0 {
		t.Errorf("the answerer held %+v and %v, want no trailer", entries, err)
	}
	if len(fake.requestPath) != 0 {
		t.Errorf("the answerer made %v, want no call", fake.requestPath)
	}
}
