package main

// What these tests read: the file each Fanart.tv art type lands under for a
// movie and for a series, the season art narrowed to one season, the list each
// fact reads where the provider holds two of them, and the miss a series with
// no TheTVDB id answers.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// The fake Fanart.tv the art tests run against: the art of one title on the
// lookup paths the test names, and the path itself as the bytes of every
// image, so a download in a test reaches no other server and the file says
// which image landed.
func newArtFanart(t *testing.T, answers map[string]string) (*fanartClient, *fakeFanart) {
	t.Helper()
	fake := &fakeFanart{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mutex.Lock()
		fake.paths = append(fake.paths, r.URL.Path)
		fake.requests = append(fake.requests, r.URL.Query())
		fake.mutex.Unlock()
		if body, held := answers[r.URL.Path]; held {
			_, _ = io.WriteString(w, body)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v3/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, r.URL.Path)
	}))
	t.Cleanup(server.Close)

	client := newFanartClient(server.URL, "a-key")
	client.http = server.Client()
	return client, fake
}

// The art of one movie, one image of every type this project writes, with
// every address on the fake itself.
func fanartMovieArt(base string) string {
	return `{"name":"The Signal","tmdb_id":"603","imdb_id":"tt2910814",
		"movieposter":[{"id":"1","url":"` + base + `/art/movieposter.jpg","lang":"en","likes":"7"}],
		"moviebackground":[{"id":"2","url":"` + base + `/art/moviebackground.jpg","lang":"","likes":"3"}],
		"hdmovielogo":[{"id":"3","url":"` + base + `/art/hdmovielogo.png","lang":"en","likes":"5"}],
		"hdmovieclearart":[{"id":"4","url":"` + base + `/art/hdmovieclearart.png","lang":"en","likes":"2"}],
		"moviebanner":[{"id":"5","url":"` + base + `/art/moviebanner.jpg","lang":"en","likes":"1"}],
		"moviethumb":[{"id":"6","url":"` + base + `/art/moviethumb.jpg","lang":"en","likes":"1"}],
		"moviedisc":[{"id":"7","url":"` + base + `/art/moviedisc.png","lang":"en","likes":"1","disc":"1","disc_type":"bluray"}]}`
}

// The art of one series, with the art of every season beside the art of season
// one, so a test reads both the narrowing and the fallback.
func fanartSeriesArt(base string) string {
	return `{"name":"Quiet Harbor","thetvdb_id":"81189",
		"tvposter":[{"id":"1","url":"` + base + `/art/tvposter.jpg","lang":"en","likes":"4"}],
		"showbackground":[{"id":"2","url":"` + base + `/art/showbackground.jpg","lang":"","likes":"2"}],
		"hdtvlogo":[{"id":"3","url":"` + base + `/art/hdtvlogo.png","lang":"en","likes":"3"}],
		"hdclearart":[{"id":"4","url":"` + base + `/art/hdclearart.png","lang":"en","likes":"2"}],
		"tvbanner":[{"id":"5","url":"` + base + `/art/tvbanner.jpg","lang":"en","likes":"1"}],
		"tvthumb":[{"id":"6","url":"` + base + `/art/tvthumb.jpg","lang":"en","likes":"1"}],
		"seasonposter":[{"id":"7","url":"` + base + `/art/seasonposter-1.jpg","lang":"en","likes":"1","season":"1"},
		{"id":"8","url":"` + base + `/art/seasonposter-all.jpg","lang":"en","likes":"1","season":"all"}],
		"seasonbanner":[{"id":"9","url":"` + base + `/art/seasonbanner-1.jpg","lang":"en","likes":"1","season":"1"},
		{"id":"10","url":"` + base + `/art/seasonbanner-all.jpg","lang":"en","likes":"1","season":"all"}]}`
}

// The sidecar the identity fact left in a series folder, which is where the
// art phase reads the TheTVDB id Fanart.tv keys on.
func writeSeriesSidecar(t *testing.T, root, folder, tvdb string) {
	t.Helper()
	writeFile(t, filepath.Join(root, folder, seriesSidecarName),
		`<tvshow><uniqueid type="tvdb">`+tvdb+`</uniqueid></tvshow>`)
}

// Every art type Fanart.tv holds for a movie lands under the name Kodi and
// Jellyfin read, and the ledger names the provider that answered.
func TestEachFanartMovieTypeLandsUnderItsName(t *testing.T) {
	folder := "The Signal (2014)"
	cases := []struct {
		fact string
		file string
		want string
	}{
		{fact: factPoster, file: "poster.jpg", want: "/art/movieposter.jpg"},
		{fact: factBackdrop, file: "fanart.jpg", want: "/art/moviebackground.jpg"},
		{fact: factLogo, file: "clearlogo.png", want: "/art/hdmovielogo.png"},
		{fact: factClearart, file: "clearart.png", want: "/art/hdmovieclearart.png"},
		{fact: factBanner, file: "banner.jpg", want: "/art/moviebanner.jpg"},
		{fact: factLandscape, file: "landscape.jpg", want: "/art/moviethumb.jpg"},
		{fact: factDiscart, file: "disc.png", want: "/art/moviedisc.png"},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			writeFile(t, filepath.Join(root, folder, "The Signal (2014).mkv"), "video")
			seedArtMovie(t, catalog, folder)
			work, _ := testEnricher(t, libraryKindMovies, root, catalog)
			answers := map[string]string{}
			client, _ := newArtFanart(t, answers)
			answers[fanartMoviePath+"603"] = fanartMovieArt(client.base)

			if err := work.artGap(t.Context(), test.fact, artLineOf(fanartArtAnswerer{client: client})); err != nil {
				t.Fatal(err)
			}

			if got := readFileString(t, filepath.Join(root, folder, test.file)); got != test.want {
				t.Errorf("%s holds %q, want the image at %q", test.file, got, test.want)
			}
			ledger := artLedger(t, filepath.Join(root, folder), test.fact)
			if len(ledger.Items) != 1 || !ledger.Items[0].Provider.is(providerBlockFanart) {
				t.Errorf("ledger items = %+v, want the provider that answered", ledger.Items)
			}
		})
	}
}

// Every art type Fanart.tv holds for a series lands under its own name, read
// on the TheTVDB id the sidecar carries, and the season art lands in the
// series folder under the season's own name.
func TestEachFanartSeriesTypeLandsUnderItsName(t *testing.T) {
	folder := "Quiet Harbor (2008)"
	cases := []struct {
		fact string
		file string
		want string
	}{
		{fact: factPoster, file: "poster.jpg", want: "/art/tvposter.jpg"},
		{fact: factBackdrop, file: "fanart.jpg", want: "/art/showbackground.jpg"},
		{fact: factLogo, file: "clearlogo.png", want: "/art/hdtvlogo.png"},
		{fact: factClearart, file: "clearart.png", want: "/art/hdclearart.png"},
		{fact: factBanner, file: "banner.jpg", want: "/art/tvbanner.jpg"},
		{fact: factLandscape, file: "landscape.jpg", want: "/art/tvthumb.jpg"},
		{fact: factSeasonPoster, file: "season01-poster.jpg", want: "/art/seasonposter-1.jpg"},
		{fact: factSeasonBanner, file: "season01-banner.jpg", want: "/art/seasonbanner-1.jpg"},
	}
	for _, test := range cases {
		t.Run(test.fact, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			root := t.TempDir()
			writeFile(t, filepath.Join(root, folder, "Season 01", "Quiet Harbor - S01E05.mkv"), "video")
			writeSeriesSidecar(t, root, folder, "81189")
			seedArtSeries(t, catalog, folder, []int{1})
			work, _ := testEnricher(t, libraryKindSeries, root, catalog)
			answers := map[string]string{}
			client, _ := newArtFanart(t, answers)
			answers[fanartSeriesPath+"81189"] = fanartSeriesArt(client.base)

			if err := work.artGap(t.Context(), test.fact, artLineOf(fanartArtAnswerer{client: client})); err != nil {
				t.Fatal(err)
			}

			if got := readFileString(t, filepath.Join(root, folder, test.file)); got != test.want {
				t.Errorf("%s holds %q, want the image at %q", test.file, got, test.want)
			}
		})
	}
}

// A season the provider holds no art of its own for takes the art that covers
// every season, which Fanart.tv marks with the word all.
func TestASeasonWithoutItsOwnArtTakesTheArtOfEverySeason(t *testing.T) {
	folder := "Quiet Harbor (2008)"
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, folder, "Season 03", "Quiet Harbor - S03E05.mkv"), "video")
	writeSeriesSidecar(t, root, folder, "81189")
	seedArtSeries(t, catalog, folder, []int{3})
	work, _ := testEnricher(t, libraryKindSeries, root, catalog)
	answers := map[string]string{}
	client, _ := newArtFanart(t, answers)
	answers[fanartSeriesPath+"81189"] = fanartSeriesArt(client.base)

	if err := work.artGap(t.Context(), factSeasonPoster, artLineOf(fanartArtAnswerer{client: client})); err != nil {
		t.Fatal(err)
	}

	got := readFileString(t, filepath.Join(root, folder, "season03-poster.jpg"))
	if got != "/art/seasonposter-all.jpg" {
		t.Errorf("season03-poster.jpg holds %q, want the art of every season", got)
	}
}

// A series with no TheTVDB id cannot be asked about, so the fact makes no
// call, writes nothing, and records a miss with a date.
func TestASeriesWithNoTheTVDBIDIsAMissWithADate(t *testing.T) {
	folder := "Quiet Harbor (2008)"
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, folder, "Season 01", "Quiet Harbor - S01E05.mkv"), "video")
	writeFile(t, filepath.Join(root, folder, seriesSidecarName),
		`<tvshow><uniqueid type="tmdb">1396</uniqueid></tvshow>`)
	seedArtSeries(t, catalog, folder, []int{1})
	work, _ := testEnricher(t, libraryKindSeries, root, catalog)
	answers := map[string]string{}
	client, fake := newArtFanart(t, answers)
	answers[fanartSeriesPath+"81189"] = fanartSeriesArt(client.base)

	if err := work.artGap(t.Context(), factClearart, artLineOf(fanartArtAnswerer{client: client})); err != nil {
		t.Fatal(err)
	}

	if fileExistsInTest(t, filepath.Join(root, folder, "clearart.png")) {
		t.Error("the fact wrote a file, want none")
	}
	if len(fake.paths) != 0 {
		t.Errorf("the fact asked %v, want no call", fake.paths)
	}
	ledger := artLedger(t, filepath.Join(root, folder), factClearart)
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptNothing {
		t.Errorf("ledger attempts = %+v, want a miss with a date", ledger.Attempts)
	}
}

// The logo and the clearart read the high-definition list where the provider
// holds one, and the list beside it where it holds none.
func TestTheHighDefinitionListComesFirst(t *testing.T) {
	sd := []fanartImage{{ID: "2", URL: "/sd.png"}}
	movie := &fanartMovie{Logos: sd, Clearart: sd}
	series := &fanartSeries{Clearlogos: sd, Clearart: sd}

	cases := []struct {
		name string
		held []fanartImage
		want string
	}{
		{name: "a movie logo", held: fanartMovieImages(movie, factLogo), want: "/sd.png"},
		{name: "a movie clearart", held: fanartMovieImages(movie, factClearart), want: "/sd.png"},
		{name: "a series logo", held: fanartSeriesImages(series, factLogo, 0), want: "/sd.png"},
		{name: "a series clearart", held: fanartSeriesImages(series, factClearart, 0), want: "/sd.png"},
		{name: "a movie season poster", held: fanartMovieImages(movie, factSeasonPoster)},
		{name: "a movie with no logo of any kind", held: fanartMovieImages(&fanartMovie{}, factLogo)},
		{name: "a series disc", held: fanartSeriesImages(series, factDiscart, 0)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if test.want == "" {
				if len(test.held) != 0 {
					t.Errorf("images = %+v, want none", test.held)
				}
				return
			}
			if len(test.held) != 1 || test.held[0].URL != test.want {
				t.Errorf("images = %+v, want %q", test.held, test.want)
			}
		})
	}
}

// A movie reads on the TMDb id the gap carries, and on the ids the sidecar
// holds where the gap carries none.
func TestWhichIDFanartReadsAMovieOn(t *testing.T) {
	cases := []struct {
		name  string
		gap   artGap
		title titleRef
		want  string
	}{
		{name: "the id the gap carries", gap: artGap{tmdb: "603"}, want: "603"},
		{name: "the id the sidecar carries", title: titleRef{ids: providerIDs{"tmdb": "550"}}, want: "550"},
		{name: "the IMDb id where there is no other",
			title: titleRef{ids: providerIDs{"imdb": "tt2910814"}}, want: "tt2910814"},
		{name: "no id at all"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := fanartMovieKey(test.gap, test.title); got != test.want {
				t.Errorf("key = %q, want %q", got, test.want)
			}
		})
	}
}

// A title Fanart.tv does not hold is no image and no error, a call it refuses
// is an error the fact records, and an image it states no address for is no
// image.
func TestWhatTheFanartAnswererDoesWithAMissAndARefusal(t *testing.T) {
	answers := map[string]string{}
	client, _ := newArtFanart(t, answers)
	answerer := fanartArtAnswerer{client: client}
	movie := titleRef{kind: libraryKindMovies}
	series := titleRef{kind: libraryKindSeries, ids: providerIDs{"tvdb": "81189"}}

	for _, title := range []titleRef{movie, series} {
		candidates, err := answerer.candidates(t.Context(), factPoster, artGap{tmdb: "603"}, title)
		if err != nil || len(candidates) != 0 {
			t.Errorf("the answerer held %+v and %v, want a miss", candidates, err)
		}
	}
	if candidates, err := answerer.candidates(t.Context(), factPoster, artGap{}, movie); err != nil ||
		len(candidates) != 0 {
		t.Errorf("the answerer held %+v and %v for a title with no id, want a miss", candidates, err)
	}

	refused, _ := newFakeFanart(t, http.StatusInternalServerError, "")
	refuses := fanartArtAnswerer{client: refused}
	for _, title := range []titleRef{movie, series} {
		if _, err := refuses.candidates(t.Context(), factPoster, artGap{tmdb: "603"}, title); err == nil {
			t.Error("the answerer reported no error, want one")
		}
	}

	if got := fanartCandidates([]fanartImage{{ID: "1", Likes: "2"}}); len(got) != 0 {
		t.Errorf("candidates = %+v, want none", got)
	}
}
