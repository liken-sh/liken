package main

// What these tests read: the query TheIntroDB is asked for a movie and for an
// episode, the spans its answer becomes, the miss a 404 is, the key that
// travels only where the account names one, and the two cooldowns its rate
// headers name.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// What one fake TheIntroDB recorded, and what it answers: one status and body
// for every request, and the headers of a 429 for the first requests a test
// names.
type fakeTheIntroDB struct {
	mutex     sync.Mutex
	requests  []*http.Request
	status    int
	body      string
	tooMany   int
	limited   http.Header
	cooldowns []time.Duration
}

func (f *fakeTheIntroDB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.requests = append(f.requests, r)
	if f.tooMany > 0 {
		f.tooMany--
		for name, values := range f.limited {
			w.Header()[name] = values
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"Too Many Requests"}`)
		return
	}
	w.WriteHeader(f.status)
	_, _ = io.WriteString(w, f.body)
}

// The client and the fake it reads, with the cooldown recorded and not slept.
func newFakeTheIntroDB(t *testing.T, token string, status int, body string) (*theintrodbClient, *fakeTheIntroDB) {
	t.Helper()
	fake := &fakeTheIntroDB{status: status, body: body}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := newTheIntroDBClient(server.URL, token)
	client.http = server.Client()
	client.wait = func(_ context.Context, cooldown time.Duration) error {
		fake.cooldowns = append(fake.cooldowns, cooldown)
		return nil
	}
	return client, fake
}

// The answer TheIntroDB documents for an episode: three candidate intros,
// one with a null start, and one credits span.
const theintrodbEpisodeAnswer = `{"tmdb_id":1399,"type":"tv","season":1,"episode":2,
	"intro":[{"start_ms":null,"end_ms":107000},{"start_ms":7007,"end_ms":106482},{"start_ms":8000,"end_ms":109000}],
	"credits":[{"start_ms":3253000,"end_ms":3316000}]}`

func milliseconds(value int64) *int64 { return &value }

// An episode is asked by the series' TMDb id, the two aired numbers, and the
// file's length, and every candidate comes back in the answer's order with
// its null ends absent.
func TestTheIntroDBAnswersEveryCandidateOfAnEpisode(t *testing.T) {
	client, fake := newFakeTheIntroDB(t, "", http.StatusOK, theintrodbEpisodeAnswer)
	answerer := newTheIntroDBMarkAnswerer(client)

	entries, err := answerer.marks(t.Context(), markFile{
		ids: providerIDs{"tmdb": "1399"}, season: 1, episode: 2, duration: 3318000,
	})

	if err != nil {
		t.Fatal(err)
	}
	want := []markEntry{
		{Kind: markKindIntro, End: milliseconds(107000), Source: providerBlockTheIntroDB},
		{Kind: markKindIntro, Start: milliseconds(7007), End: milliseconds(106482), Source: providerBlockTheIntroDB},
		{Kind: markKindIntro, Start: milliseconds(8000), End: milliseconds(109000), Source: providerBlockTheIntroDB},
		{Kind: markKindCredits, Start: milliseconds(3253000), End: milliseconds(3316000), Source: providerBlockTheIntroDB},
	}
	if !slices.EqualFunc(entries, want, sameMark) {
		t.Errorf("entries = %v, want %v", markText(entries), markText(want))
	}
	query := fake.requests[0].URL.Query()
	if fake.requests[0].URL.Path != theintrodbMediaPath {
		t.Errorf("the ask went to %s, want %s", fake.requests[0].URL.Path, theintrodbMediaPath)
	}
	for name, value := range map[string]string{
		"tmdb_id": "1399", "season": "1", "episode": "2", "duration_ms": "3318000",
	} {
		if query.Get(name) != value {
			t.Errorf("the ask named %s=%q, want %q", name, query.Get(name), value)
		}
	}
}

// A movie is asked by its own TMDb id alone, and the four kinds come back in
// the order intro, recap, credits, preview.
func TestTheIntroDBAsksAMovieByItsIDAlone(t *testing.T) {
	client, fake := newFakeTheIntroDB(t, "", http.StatusOK, `{"tmdb_id":603,"type":"movie",
		"preview":[{"start_ms":1680000,"end_ms":1740000}],
		"credits":[{"start_ms":8000000,"end_ms":null}],
		"recap":[{"start_ms":25000,"end_ms":134000}],
		"intro":[{"start_ms":null,"end_ms":23000}]}`)

	entries, err := newTheIntroDBMarkAnswerer(client).marks(t.Context(), markFile{
		movie: true, ids: providerIDs{"tmdb": "603"},
	})

	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, entry := range entries {
		kinds = append(kinds, entry.Kind)
	}
	if !slices.Equal(kinds, []string{markKindIntro, markKindRecap, markKindCredits, markKindPreview}) {
		t.Errorf("kinds = %v, want intro, recap, credits, preview", kinds)
	}
	query := fake.requests[0].URL.Query()
	for _, absent := range []string{"season", "episode", "duration_ms"} {
		if query.Has(absent) {
			t.Errorf("the ask named %s for a movie with no length, want none", absent)
		}
	}
}

// A 404 is TheIntroDB saying it holds no span for the work, which is a miss
// and not an error. A work with no TMDb id is never asked.
func TestTheIntroDBMisses(t *testing.T) {
	cases := []struct {
		name     string
		ids      providerIDs
		asked    int
		wantNone bool
	}{
		{name: "a work TheIntroDB does not hold", ids: providerIDs{"tmdb": "1399"}, asked: 1},
		{name: "a work with no TMDb id", ids: providerIDs{"imdb": "tt0944947"}, asked: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, fake := newFakeTheIntroDB(t, "", http.StatusNotFound, `{"error":"media not found"}`)

			entries, err := newTheIntroDBMarkAnswerer(client).marks(t.Context(),
				markFile{ids: test.ids, season: 9, episode: 9})

			if err != nil || len(entries) != 0 {
				t.Errorf("marks = %v, %v, want no span and no error", entries, err)
			}
			if len(fake.requests) != test.asked {
				t.Errorf("the client made %d requests, want %d", len(fake.requests), test.asked)
			}
		})
	}
}

// Any other status is an error that carries TheIntroDB's own words.
func TestATheIntroDBFailureCarriesItsBody(t *testing.T) {
	client, _ := newFakeTheIntroDB(t, "", http.StatusBadRequest, `{"error":"invalid season"}`)

	_, err := newTheIntroDBMarkAnswerer(client).marks(t.Context(),
		markFile{ids: providerIDs{"tmdb": "1399"}, season: 1, episode: 2})

	if err == nil || !strings.Contains(err.Error(), `{"error":"invalid season"}`) {
		t.Errorf("err = %v, want the body TheIntroDB answered", err)
	}
}

// The key is optional. An account that names one sends it as a bearer token,
// and one that names none sends no header at all.
func TestTheIntroDBSendsTheKeyOnlyWhereTheAccountNamesOne(t *testing.T) {
	cases := []struct {
		name  string
		token string
		want  string
	}{
		{name: "an account with a key", token: "a-key", want: "Bearer a-key"},
		{name: "an anonymous account", token: "", want: ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, fake := newFakeTheIntroDB(t, test.token, http.StatusOK, `{"tmdb_id":603,"type":"movie"}`)

			if _, err := newTheIntroDBMarkAnswerer(client).marks(t.Context(),
				markFile{movie: true, ids: providerIDs{"tmdb": "603"}}); err != nil {
				t.Fatal(err)
			}
			if got := fake.requests[0].Header.Get("Authorization"); got != test.want {
				t.Errorf("Authorization = %q, want %q", got, test.want)
			}
		})
	}
}

// A 429 that spends the window waits for the window's own reset and asks
// again. A 429 that spends the day's allowance waits for nothing, because the
// reset is hours away: the ask fails with TheIntroDB's words, and the file
// waits for a later run.
func TestTheIntroDBCooldownsFollowItsRateHeaders(t *testing.T) {
	cases := []struct {
		name      string
		limited   http.Header
		wantWaits []time.Duration
		wantError bool
	}{
		{
			name: "the ten-second window is spent",
			limited: http.Header{
				"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"4"},
				"X-Usagelimit-Remaining": {"812"}, "X-Usagelimit-Reset": {"0"},
			},
			wantWaits: []time.Duration{4 * time.Second},
		},
		{
			name: "the day's allowance is spent",
			limited: http.Header{
				"X-Ratelimit-Remaining": {"29"}, "X-Ratelimit-Reset": {"10"},
				"X-Usagelimit-Remaining": {"0"}, "X-Usagelimit-Reset": {"30240"},
			},
			wantError: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, fake := newFakeTheIntroDB(t, "", http.StatusOK, `{"tmdb_id":603,"type":"movie"}`)
			fake.tooMany, fake.limited = 1, test.limited

			_, err := newTheIntroDBMarkAnswerer(client).marks(t.Context(),
				markFile{movie: true, ids: providerIDs{"tmdb": "603"}})

			if test.wantError != (err != nil) {
				t.Errorf("err = %v, want an error: %v", err, test.wantError)
			}
			if test.wantError && !answeredWith(err, http.StatusTooManyRequests) {
				t.Errorf("err = %v, want the 429 TheIntroDB answered", err)
			}
			if !slices.Equal(fake.cooldowns, test.wantWaits) {
				t.Errorf("cooldowns = %v, want %v", fake.cooldowns, test.wantWaits)
			}
		})
	}
}

// Two entries are the same span when every field holds the same value, and
// an absent end is not an end of zero.
func sameMark(a, b markEntry) bool {
	return a.Path == b.Path && a.Kind == b.Kind && a.Source == b.Source &&
		sameEnd(a.Start, b.Start) && sameEnd(a.End, b.End)
}

func sameEnd(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// The spans as one line each, so a failure reads as values and not pointers.
func markText(entries []markEntry) []string {
	lines := []string{}
	for _, entry := range entries {
		lines = append(lines, entry.Path+" "+entry.Kind+" "+endText(entry.Start)+"-"+endText(entry.End)+" "+entry.Source)
	}
	return lines
}

func endText(end *int64) string {
	if end == nil {
		return "open"
	}
	return time.Duration(*end * int64(time.Millisecond)).String()
}
