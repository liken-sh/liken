package main

// PROSE: what these tests read: the art lists Fanart.tv answers for a movie
// and for a series, the season art of one season, the key the request
// carries, and the miss a title it does not hold answers.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// PROSE: what one fake Fanart.tv recorded: the path and the query of every
// request.
type fakeFanart struct {
	mutex    sync.Mutex
	paths    []string
	requests []url.Values
}

// The client and the fake it reads, so no test reaches the real Fanart.tv.
func newFakeFanart(t *testing.T, status int, body string) (*fanartClient, *fakeFanart) {
	t.Helper()
	fake := &fakeFanart{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mutex.Lock()
		fake.paths = append(fake.paths, r.URL.Path)
		fake.requests = append(fake.requests, r.URL.Query())
		fake.mutex.Unlock()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	client := newFanartClient(server.URL, "a-key")
	client.http = server.Client()
	return client, fake
}

// PROSE: one movie answer, with one image of every type the art facts write.
const fanartMovieAnswer = `{"name":"Fight Club","tmdb_id":"550","imdb_id":"tt0137523",
	"movieposter":[{"id":"1","url":"https://assets.fanart.tv/fanart/movies/550/movieposter/a.jpg","lang":"en","likes":"7"}],
	"moviebackground":[{"id":"2","url":"https://assets.fanart.tv/fanart/movies/550/moviebackground/b.jpg","lang":"","likes":"3"}],
	"hdmovielogo":[{"id":"3","url":"https://assets.fanart.tv/fanart/movies/550/hdmovielogo/c.png","lang":"en","likes":"5"}],
	"hdmovieclearart":[{"id":"4","url":"https://assets.fanart.tv/fanart/movies/550/hdmovieclearart/d.png","lang":"en","likes":"2"}],
	"moviebanner":[{"id":"5","url":"https://assets.fanart.tv/fanart/movies/550/moviebanner/e.jpg","lang":"en","likes":"1"}],
	"moviethumb":[{"id":"6","url":"https://assets.fanart.tv/fanart/movies/550/moviethumb/f.jpg","lang":"en","likes":"1"}],
	"moviedisc":[{"id":"7","url":"https://assets.fanart.tv/fanart/movies/550/moviedisc/g.png","lang":"en","likes":"1","disc":"1","disc_type":"bluray"}]}`

// PROSE: the movie call reads every art type at once, and the key travels as
// the api_key parameter.
func TestTheFanartMovieCallReadsEveryArtType(t *testing.T) {
	client, fake := newFakeFanart(t, http.StatusOK, fanartMovieAnswer)

	movie, err := client.movie(t.Context(), "550")
	if err != nil {
		t.Fatal(err)
	}

	if len(movie.Posters) != 1 || len(movie.Backgrounds) != 1 || len(movie.HDLogos) != 1 {
		t.Errorf("movie = %+v, want the poster, the background, and the logo", movie)
	}
	if len(movie.HDClearart) != 1 || len(movie.Banners) != 1 || len(movie.Thumbs) != 1 {
		t.Errorf("movie = %+v, want the clearart, the banner, and the landscape", movie)
	}
	if len(movie.Discs) != 1 || movie.Discs[0].DiscType != "bluray" {
		t.Errorf("discs = %+v, want the disc art and the kind of disc", movie.Discs)
	}
	if movie.Posters[0].URL == "" || movie.Posters[0].Lang != "en" || movie.Posters[0].Likes != "7" {
		t.Errorf("poster = %+v, want its address, its language, and its likes", movie.Posters[0])
	}
	if fake.paths[0] != fanartMoviePath+"550" || fake.requests[0].Get(fanartAPIKeyParam) != "a-key" {
		t.Errorf("the call asked %s with %v, want the movie and the key",
			fake.paths[0], fake.requests[0])
	}
}

// PROSE: one series answer, with art of the whole show and art of one season.
const fanartSeriesAnswer = `{"name":"Twin Peaks","thetvdb_id":"70533",
	"tvposter":[{"id":"1","url":"https://assets.fanart.tv/fanart/tv/70533/tvposter/a.jpg","lang":"en","likes":"4"}],
	"tvbanner":[{"id":"2","url":"https://assets.fanart.tv/fanart/tv/70533/tvbanner/b.jpg","lang":"en","likes":"2"}],
	"seasonposter":[{"id":"3","url":"https://assets.fanart.tv/fanart/tv/70533/seasonposter/c.jpg","lang":"en","likes":"1","season":"1"},
	{"id":"4","url":"https://assets.fanart.tv/fanart/tv/70533/seasonposter/d.jpg","lang":"en","likes":"1","season":"2"},
	{"id":"5","url":"https://assets.fanart.tv/fanart/tv/70533/seasonposter/e.jpg","lang":"en","likes":"1","season":"all"}],
	"seasonbanner":[{"id":"6","url":"https://assets.fanart.tv/fanart/tv/70533/seasonbanner/f.jpg","lang":"en","likes":"1","season":"1"}]}`

// PROSE: the series call reads on the TheTVDB id, and the season art of one
// season is that season's own art with the art that covers every season.
func TestTheFanartSeriesCallReadsTheSeasonArt(t *testing.T) {
	client, fake := newFakeFanart(t, http.StatusOK, fanartSeriesAnswer)

	series, err := client.series(t.Context(), "70533")
	if err != nil {
		t.Fatal(err)
	}

	if len(series.Posters) != 1 || len(series.Banners) != 1 || len(series.SeasonBanners) != 1 {
		t.Errorf("series = %+v, want the poster, the banner, and the season banner", series)
	}
	if fake.paths[0] != fanartSeriesPath+"70533" {
		t.Errorf("the call asked %s, want the series", fake.paths[0])
	}

	cases := []struct {
		name   string
		season string
		want   int
	}{
		{name: "a season with art of its own", season: "1", want: 2},
		{name: "a season with the art of every season alone", season: "3", want: 1},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := seasonImages(series.SeasonPosters, one.season); len(got) != one.want {
				t.Errorf("season posters = %+v, want %d", got, one.want)
			}
		})
	}
}

// PROSE: a title Fanart.tv does not hold answers 404, which is a miss and not
// an error, because a title with no art is the ordinary case.
func TestAFanartTitleWithNoArt(t *testing.T) {
	client, _ := newFakeFanart(t, http.StatusNotFound, `{"status":"error","error message":"Not found"}`)

	movie, err := client.movie(t.Context(), "550")
	if err != nil || movie != nil {
		t.Errorf("the movie call answered %+v and %v, want a miss", movie, err)
	}
	series, err := client.series(t.Context(), "70533")
	if err != nil || series != nil {
		t.Errorf("the series call answered %+v and %v, want a miss", series, err)
	}
}

// PROSE: an answer that is neither the art nor a miss is an error the attempt
// records, so the next run tries again.
func TestAFanartCallThatFails(t *testing.T) {
	client, _ := newFakeFanart(t, http.StatusInternalServerError, "")

	if _, err := client.movie(t.Context(), "550"); err == nil {
		t.Error("the movie call read no error from a 500")
	}
	if _, err := client.series(t.Context(), "70533"); err == nil {
		t.Error("the series call read no error from a 500")
	}
}
