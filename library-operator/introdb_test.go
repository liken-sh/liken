package main

// What these tests read: the query IntroDB is asked for a movie and for an
// episode, the spans its answer becomes under this fact's own kinds, and the
// miss an answer of nulls is.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// What one fake IntroDB recorded, and the one answer it gives.
type fakeIntroDB struct {
	mutex    sync.Mutex
	requests []*http.Request
	status   int
	body     string
}

func (f *fakeIntroDB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.requests = append(f.requests, r)
	w.WriteHeader(f.status)
	_, _ = io.WriteString(w, f.body)
}

func newFakeIntroDB(t *testing.T, status int, body string) (*introdbClient, *fakeIntroDB) {
	t.Helper()
	fake := &fakeIntroDB{status: status, body: body}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := newIntroDBClient(server.URL)
	client.http = server.Client()
	return client, fake
}

// The answer IntroDB gives for an episode: one span per kind, or null.
const introdbEpisodeAnswer = `{"imdb_id":"tt0944947","media_type":"tv","is_movie":false,"season":1,"episode":1,
	"intro":{"start_sec":437,"end_sec":531,"start_ms":437000,"end_ms":531000,"confidence":1,"submission_count":2},
	"recap":null,
	"outro":{"start_sec":3631.5,"end_sec":3699.5,"start_ms":3631500,"end_ms":3699500,"confidence":1,"submission_count":2},
	"post_credits":null}`

// An episode is asked by the series' IMDb id and the two aired numbers, and
// IntroDB's outro is this fact's credits.
func TestIntroDBAnswersTheSpansOfAnEpisode(t *testing.T) {
	client, fake := newFakeIntroDB(t, http.StatusOK, introdbEpisodeAnswer)

	entries, err := newIntroDBMarkAnswerer(client).marks(t.Context(), markFile{
		ids: providerIDs{"imdb": "tt0944947", "tmdb": "1399"}, season: 1, episode: 1, duration: 3700000,
	})

	if err != nil {
		t.Fatal(err)
	}
	want := []markEntry{
		{Kind: markKindIntro, Start: milliseconds(437000), End: milliseconds(531000), Source: providerBlockIntroDB},
		{Kind: markKindCredits, Start: milliseconds(3631500), End: milliseconds(3699500), Source: providerBlockIntroDB},
	}
	if !slices.EqualFunc(entries, want, sameMark) {
		t.Errorf("entries = %v, want %v", markText(entries), markText(want))
	}
	request := fake.requests[0]
	if request.URL.Path != introdbSegmentsPath {
		t.Errorf("the ask went to %s, want %s", request.URL.Path, introdbSegmentsPath)
	}
	query := request.URL.Query()
	for name, value := range map[string]string{"imdb_id": "tt0944947", "season": "1", "episode": "1"} {
		if query.Get(name) != value {
			t.Errorf("the ask named %s=%q, want %q", name, query.Get(name), value)
		}
	}
}

// A movie is asked by its IMDb id and the movie flag, and a post-credits
// scene keeps a kind of its own.
func TestIntroDBAsksAMovieWithTheMovieFlag(t *testing.T) {
	client, fake := newFakeIntroDB(t, http.StatusOK, `{"imdb_id":"tt0133093","is_movie":true,
		"intro":null,"recap":null,
		"outro":{"start_ms":7800000,"end_ms":8100000},
		"post_credits":{"start_ms":8100000,"end_ms":8160000}}`)

	entries, err := newIntroDBMarkAnswerer(client).marks(t.Context(),
		markFile{movie: true, ids: providerIDs{"imdb": "tt0133093"}})

	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, entry := range entries {
		kinds = append(kinds, entry.Kind)
	}
	if !slices.Equal(kinds, []string{markKindCredits, markKindPostCredits}) {
		t.Errorf("kinds = %v, want credits and post-credits", kinds)
	}
	query := fake.requests[0].URL.Query()
	if query.Get("is_movie") != "true" || query.Has("season") || query.Has("episode") {
		t.Errorf("the ask named %v, want the movie flag and no numbers", query)
	}
}

// An answer of nulls, a 404, and a work with no IMDb id are all a miss and
// not an error.
func TestIntroDBMisses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		ids    providerIDs
		asked  int
	}{
		{
			name: "an answer of nulls", status: http.StatusOK,
			body: `{"imdb_id":"tt0944947","intro":null,"recap":null,"outro":null,"post_credits":null}`,
			ids:  providerIDs{"imdb": "tt0944947"}, asked: 1,
		},
		{
			name: "a 404", status: http.StatusNotFound, body: `{"error":"not found"}`,
			ids: providerIDs{"imdb": "tt0944947"}, asked: 1,
		},
		{name: "a work with no IMDb id", status: http.StatusOK, ids: providerIDs{"tmdb": "1399"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, fake := newFakeIntroDB(t, test.status, test.body)

			entries, err := newIntroDBMarkAnswerer(client).marks(t.Context(),
				markFile{ids: test.ids, season: 1, episode: 1})

			if err != nil || len(entries) != 0 {
				t.Errorf("marks = %v, %v, want no span and no error", entries, err)
			}
			if len(fake.requests) != test.asked {
				t.Errorf("the client made %d requests, want %d", len(fake.requests), test.asked)
			}
		})
	}
}

// Any other status is an error that carries IntroDB's own words.
func TestAnIntroDBFailureCarriesItsBody(t *testing.T) {
	client, _ := newFakeIntroDB(t, http.StatusBadRequest, `{"error":"Invalid query params."}`)

	_, err := newIntroDBMarkAnswerer(client).marks(t.Context(),
		markFile{ids: providerIDs{"imdb": "tt0944947"}, season: 1, episode: 1})

	if err == nil || !strings.Contains(err.Error(), `{"error":"Invalid query params."}`) {
		t.Errorf("err = %v, want the body IntroDB answered", err)
	}
}
