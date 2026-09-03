package main

// PROSE: what these tests read: the show TVmaze answers for an id another
// provider gave, the cast and the images of that show, the miss an id it does
// not hold answers, and that no request carries a key.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// PROSE: what one fake TVmaze recorded: the path and the query of every
// request, and the answer it holds for each path.
type fakeTVmaze struct {
	mutex    sync.Mutex
	requests []*http.Request
}

// The client and the fake it reads, so no test reaches the real TVmaze.
func newFakeTVmaze(t *testing.T, status int, answers map[string]string) (*tvmazeClient, *fakeTVmaze) {
	t.Helper()
	fake := &fakeTVmaze{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mutex.Lock()
		fake.requests = append(fake.requests, r)
		fake.mutex.Unlock()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answers[r.URL.Path])
	}))
	t.Cleanup(server.Close)

	client := newTVmazeClient(server.URL)
	client.http = server.Client()
	return client, fake
}

// PROSE: one show, with the ids, the overview fields, and the poster TVmaze
// carries on the show itself.
const tvmazeShowAnswer = `{"id":1371,"name":"Twin Peaks","premiered":"1990-04-08",
	"genres":["Drama","Mystery"],"runtime":60,"averageRuntime":47,
	"summary":"<p>An FBI agent arrives in a small town.</p>",
	"rating":{"average":8.6},"network":{"id":2,"name":"ABC"},
	"image":{"medium":"https://static.tvmaze.com/uploads/m.jpg","original":"https://static.tvmaze.com/uploads/o.jpg"},
	"externals":{"imdb":"tt0098936","thetvdb":70533,"tvrage":6293}}`

// PROSE: a lookup on an id another provider gave answers the show, with the
// ids the identity fact writes and the fields the overview fact reads. TVmaze
// takes no account, so the request carries no key at all.
func TestTheTVmazeLookupReadsOneShow(t *testing.T) {
	client, fake := newFakeTVmaze(t, http.StatusOK,
		map[string]string{tvmazeLookupPath: tvmazeShowAnswer})

	show, err := client.lookup(t.Context(), tvmazeSchemeIMDb, "tt0098936")
	if err != nil {
		t.Fatal(err)
	}

	if show.ID != 1371 || show.Externals.TheTVDB != 70533 || show.Externals.IMDb != "tt0098936" {
		t.Errorf("show = %+v, want the ids of every database", show)
	}
	if show.Premiered == "" || len(show.Genres) != 2 || show.Summary == "" || show.Network.Name != "ABC" {
		t.Errorf("show = %+v, want the premiere, the genres, the summary, and the network", show)
	}
	if show.Rating.Average != 8.6 || show.Image.Original == "" || show.AverageRuntime != 47 {
		t.Errorf("show = %+v, want the rating, the image, and the runtime", show)
	}
	request := fake.requests[0]
	if got := request.URL.Query().Get(tvmazeSchemeIMDb); got != "tt0098936" {
		t.Errorf("the lookup asked for %q, want the IMDb id", got)
	}
	if request.Header.Get("Authorization") != "" || request.URL.Query().Get("api_key") != "" {
		t.Errorf("the request carried a key in %v, want none", request.URL)
	}
}

// PROSE: an id TVmaze does not hold answers 404, which is a miss and not an
// error.
func TestATVmazeLookupThatFindsNoShow(t *testing.T) {
	client, _ := newFakeTVmaze(t, http.StatusNotFound, nil)

	show, err := client.lookup(t.Context(), tvmazeSchemeTheTVDB, "1")
	if err != nil || show != nil {
		t.Errorf("the lookup answered %+v and %v, want a miss", show, err)
	}
}

// PROSE: the show, the cast, and the images of one TVmaze id, which are the
// three calls the series facts make.
func TestTheTVmazeShowCallsReadWhatTheFactsWrite(t *testing.T) {
	client, _ := newFakeTVmaze(t, http.StatusOK, map[string]string{
		tvmazeShowsPath + "1371": tvmazeShowAnswer,
		tvmazeShowsPath + "1371/cast": `[{"person":{"id":8,"name":"Kyle MacLachlan",
			"image":{"medium":"https://static.tvmaze.com/uploads/p.jpg","original":"https://static.tvmaze.com/uploads/p.jpg"}},
			"character":{"id":11,"name":"Dale Cooper"}}]`,
		tvmazeShowsPath + "1371/images": `[{"id":1,"type":"poster","main":true,
			"resolutions":{"original":{"url":"https://static.tvmaze.com/uploads/a.jpg","width":1000,"height":1500}}},
			{"id":2,"type":"background","main":false,
			"resolutions":{"original":{"url":"https://static.tvmaze.com/uploads/b.jpg","width":1920,"height":1080}}}]`,
	})

	show, err := client.show(t.Context(), 1371)
	if err != nil || show.Name != "Twin Peaks" {
		t.Fatalf("show = %+v, %v", show, err)
	}

	cast, err := client.cast(t.Context(), 1371)
	if err != nil {
		t.Fatal(err)
	}
	if len(cast) != 1 || cast[0].Person.Name != "Kyle MacLachlan" || cast[0].Character.Name != "Dale Cooper" {
		t.Errorf("cast = %+v, want the person and the character", cast)
	}
	if cast[0].Person.Image.Original == "" {
		t.Errorf("person = %+v, want the headshot", cast[0].Person)
	}

	images, err := client.images(t.Context(), 1371)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		kind string
		want int
	}{
		{name: "the posters", kind: tvmazeArtworkPoster, want: 1},
		{name: "the backgrounds", kind: tvmazeArtworkBackground, want: 1},
		{name: "the banners", kind: tvmazeArtworkBanner},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := artworkOfType(images, one.kind)
			if len(got) != one.want {
				t.Errorf("images of %s = %+v, want %d", one.kind, got, one.want)
			}
			if one.want > 0 && got[0].Resolutions.Original.URL == "" {
				t.Errorf("image = %+v, want the address of the original", got[0])
			}
		})
	}
}

// PROSE: an answer that is neither a show nor a miss is an error the attempt
// records.
func TestATVmazeCallThatFails(t *testing.T) {
	client, _ := newFakeTVmaze(t, http.StatusInternalServerError, nil)

	if _, err := client.lookup(t.Context(), tvmazeSchemeIMDb, "tt0098936"); err == nil {
		t.Error("the lookup read no error from a 500")
	}
	if _, err := client.show(t.Context(), 1371); err == nil {
		t.Error("the show call read no error from a 500")
	}
	if _, err := client.cast(t.Context(), 1371); err == nil {
		t.Error("the cast call read no error from a 500")
	}
	if _, err := client.images(t.Context(), 1371); err == nil {
		t.Error("the images call read no error from a 500")
	}
}
