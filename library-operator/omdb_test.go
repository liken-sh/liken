package main

// What these tests read: the title OMDb answers, the title it does not hold,
// the two 401 answers a key can get, and the cooldown a 429 takes.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

// What one fake OMDb recorded: the query of every request, and the cooldowns
// the client waited instead of sleeping.
type fakeOMDb struct {
	mutex     sync.Mutex
	requests  []url.Values
	cooldowns []time.Duration
}

// The client and the fake it reads, with the real cooldown taken out, so no
// test sleeps and no test reaches the real OMDb.
func newFakeOMDb(t *testing.T, answer func(url.Values) (int, string)) (*omdbClient, *fakeOMDb) {
	t.Helper()
	fake := &fakeOMDb{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mutex.Lock()
		fake.requests = append(fake.requests, r.URL.Query())
		fake.mutex.Unlock()
		status, body := answer(r.URL.Query())
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	client := newOMDbClient(server.URL, "a-key")
	client.http = server.Client()
	client.wait = func(_ context.Context, cooldown time.Duration) error {
		fake.mutex.Lock()
		defer fake.mutex.Unlock()
		fake.cooldowns = append(fake.cooldowns, cooldown)
		return nil
	}
	return client, fake
}

// One answer of the whole shape the nfo facts read.
const omdbAnswer = `{"Title":"The Godfather","Year":"1972","Rated":"R",` +
	`"Released":"24 Mar 1972","Runtime":"175 min","Genre":"Crime, Drama",` +
	`"Plot":"The aging patriarch of an organized crime dynasty.",` +
	`"Ratings":[{"Source":"Internet Movie Database","Value":"9.2/10"},` +
	`{"Source":"Rotten Tomatoes","Value":"97%"},{"Source":"Metacritic","Value":"100/100"}],` +
	`"Metascore":"100","imdbRating":"9.2","imdbVotes":"2,000,000","imdbID":"tt0068646",` +
	`"Type":"movie","Response":"True"}`

// The lookup asks for one IMDb id with the full plot, carries the key as a
// query parameter, and reads back what each nfo fact needs.
func TestTheOMDbLookupReadsOneTitle(t *testing.T) {
	client, fake := newFakeOMDb(t, func(url.Values) (int, string) {
		return http.StatusOK, omdbAnswer
	})

	title, err := client.title(t.Context(), "tt0068646")
	if err != nil {
		t.Fatal(err)
	}

	if !title.found() {
		t.Errorf("found = false, want the title OMDb answered")
	}
	if title.Rated != "R" || title.Plot == "" || title.IMDbRating != "9.2" || title.Metascore != "100" {
		t.Errorf("title = %+v, want the certification, the plot, and the ratings", title)
	}
	if got := title.score(omdbSourceRottenTomatoes); got != "97%" {
		t.Errorf("the Rotten Tomatoes score = %q, want 97%%", got)
	}
	if got := title.score(omdbSourceIMDb); got != "9.2/10" {
		t.Errorf("the IMDb score = %q, want 9.2/10", got)
	}
	if got := title.score("a site OMDb does not name"); got != "" {
		t.Errorf("an unnamed source scored %q, want nothing", got)
	}
	query := fake.requests[0]
	if query.Get("apikey") != "a-key" || query.Get("i") != "tt0068646" || query.Get("plot") != omdbFullPlot {
		t.Errorf("the lookup asked %v, want the key, the id, and the full plot", query)
	}
}

// OMDb answers 200 for a title it does not hold, so the answer alone is not a
// find, and the fact records a miss and not an error.
func TestAnOMDbTitleTheProviderDoesNotHold(t *testing.T) {
	client, _ := newFakeOMDb(t, func(url.Values) (int, string) {
		return http.StatusOK, `{"Response":"False","Error":"Incorrect IMDb ID."}`
	})

	title, err := client.title(t.Context(), "tt0000000")
	if err != nil {
		t.Fatal(err)
	}
	if title.found() {
		t.Errorf("found = true, want a miss for a title OMDb does not hold")
	}
}

// A key with no calls left ends OMDb's work for the run. A key OMDb refused
// before any call worked does not.
func TestTheOMDbDailyLimitSignal(t *testing.T) {
	cases := []struct {
		name    string
		answers []string
		status  int
		want    bool
	}{
		{name: "a key the provider refuses before any call worked",
			answers: []string{`{"Response":"False","Error":"Invalid API key!"}`},
			status:  http.StatusUnauthorized},
		{name: "a refusal that names the limit",
			answers: []string{`{"Response":"False","Error":"Request limit reached!"}`},
			status:  http.StatusUnauthorized, want: true},
		{name: "an answer that refuses nothing", answers: []string{""},
			status: http.StatusInternalServerError},
		{name: "a refusal after a call that worked",
			answers: []string{omdbAnswer, `{"Response":"False","Error":"Invalid API key!"}`},
			status:  http.StatusUnauthorized, want: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			calls := 0
			client, _ := newFakeOMDb(t, func(url.Values) (int, string) {
				answer := one.answers[calls]
				status := one.status
				if calls < len(one.answers)-1 {
					status = http.StatusOK
				}
				calls++
				return status, answer
			})

			for range one.answers {
				_, _ = client.title(t.Context(), "tt0068646")
			}

			if got := client.dailyLimitReached(); got != one.want {
				t.Errorf("dailyLimitReached = %v, want %v", got, one.want)
			}
		})
	}
}

// A 429 waits the header's own cooldown and the request goes out again, which
// is the rule every provider client shares.
func TestAnOMDbRequestWaitsOutA429(t *testing.T) {
	calls := 0
	client, fake := newFakeOMDb(t, func(url.Values) (int, string) {
		calls++
		if calls == 1 {
			return http.StatusTooManyRequests, ""
		}
		return http.StatusOK, omdbAnswer
	})

	title, err := client.title(t.Context(), "tt0068646")
	if err != nil {
		t.Fatal(err)
	}
	if !title.found() {
		t.Error("the second request read no title")
	}
	if len(fake.cooldowns) != 1 || fake.cooldowns[0] != providerCooldown {
		t.Errorf("cooldowns = %v, want one of %s", fake.cooldowns, providerCooldown)
	}
}
