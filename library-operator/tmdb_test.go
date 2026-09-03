package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

// The fake provider every identity test runs against, keyed by the request
// the client makes, so no test reaches the real TMDb.
type fakeTMDb struct {
	mutex    sync.Mutex
	answers  map[string]string
	statuses map[string]int
	served   map[string]int
	// The header a 429 answer carries, and how many of the first requests answer
	// 429 at all.
	retryAfter  string
	tooMany     int
	cooldowns   []time.Duration
	requestPath []string
	// The fake keeps the credential of the last request in both forms, so a test
	// reads which form the key travelled in.
	authorization string
	apiKey        string
}

// The one key shape a test declares its answers under: the path, the query,
// and the year the search narrowed to.
func tmdbKey(path, query, year string) string {
	return path + "|" + query + "|" + year
}

func (f *fakeTMDb) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	query := r.URL.Query()
	year := query.Get("year")
	if year == "" {
		year = query.Get("first_air_date_year")
	}
	key := tmdbKey(r.URL.Path, query.Get("query"), year)
	f.served[key]++
	f.requestPath = append(f.requestPath, key)
	f.authorization = r.Header.Get("Authorization")
	f.apiKey = query.Get("api_key")

	if f.tooMany > 0 {
		f.tooMany--
		if f.retryAfter != "" {
			w.Header().Set("Retry-After", f.retryAfter)
		}
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if status, held := f.statuses[key]; held {
		w.WriteHeader(status)
		return
	}
	answer, held := f.answers[key]
	if !held {
		answer = `{"results":[]}`
	}
	_, _ = io.WriteString(w, answer)
}

// newFakeTMDb builds the fake and the client that reads it, and takes the
// real cooldown out so no test sleeps.
func newFakeTMDb(t *testing.T, answers map[string]string) (*tmdbClient, *fakeTMDb) {
	t.Helper()
	fake := &fakeTMDb{answers: answers, statuses: map[string]int{}, served: map[string]int{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	client := newTMDbClient(server.URL, "a-token")
	client.http = server.Client()
	client.wait = func(_ context.Context, cooldown time.Duration) error {
		fake.mutex.Lock()
		defer fake.mutex.Unlock()
		fake.cooldowns = append(fake.cooldowns, cooldown)
		return nil
	}
	return client, fake
}

func TestASearchReadsTheResultsTMDbAnswers(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "The Thing", "1982"): `{"results":[{"id":1091,"title":"The Thing","original_title":"The Thing","release_date":"1982-06-25"}]}`,
		tmdbKey("/3/search/tv", "Twin Peaks", "1990"):   `{"results":[{"id":1920,"name":"Twin Peaks","original_name":"Twin Peaks","first_air_date":"1990-04-08"}]}`,
	})

	cases := []struct {
		name  string
		kind  string
		title string
		year  int
		want  int
	}{
		{name: "a movie", kind: libraryKindMovies, title: "The Thing", year: 1982, want: 1091},
		{name: "a series", kind: libraryKindSeries, title: "Twin Peaks", year: 1990, want: 1920},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			results, err := client.search(t.Context(), test.kind, test.title, test.year)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].ID != test.want {
				t.Fatalf("read %+v, want the id %d", results, test.want)
			}
			if results[0].name() == "" || results[0].originalName() == "" || results[0].year() == 0 {
				t.Errorf("read %+v, want a name, an original name, and a year", results[0])
			}
		})
	}
}

func TestARuntimeReadsFromTheDetailAnswer(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/movie/1091", "", ""): `{"runtime":109}`,
		tmdbKey("/3/tv/1920", "", ""):    `{"episode_run_time":[47]}`,
	})

	cases := []struct {
		name string
		kind string
		id   int
		want time.Duration
	}{
		{name: "a movie states one runtime", kind: libraryKindMovies, id: 1091, want: 109 * time.Minute},
		{name: "a series states its episode runtimes", kind: libraryKindSeries, id: 1920, want: 47 * time.Minute},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			runtime, err := client.runtime(t.Context(), test.kind, test.id)
			if err != nil {
				t.Fatal(err)
			}
			if runtime != test.want {
				t.Errorf("runtime = %s, want %s", runtime, test.want)
			}
		})
	}
}

func TestATooManyRequestsAnswerIsACooldownAndARetry(t *testing.T) {
	cases := []struct {
		name       string
		retryAfter string
		want       time.Duration
	}{
		{name: "the header names the wait", retryAfter: "3", want: 3 * time.Second},
		{name: "the header names none", retryAfter: "", want: providerCooldown},
		{name: "the header is not a number", retryAfter: "Wed, 21 Oct 2026 07:28:00 GMT", want: providerCooldown},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, fake := newFakeTMDb(t, map[string]string{
				tmdbKey("/3/search/movie", "The Thing", "1982"): `{"results":[{"id":1091,"title":"The Thing","release_date":"1982-06-25"}]}`,
			})
			fake.tooMany, fake.retryAfter = 1, test.retryAfter

			results, err := client.search(t.Context(), libraryKindMovies, "The Thing", 1982)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 {
				t.Fatalf("read %+v, want the retry to have answered", results)
			}
			if len(fake.cooldowns) != 1 || fake.cooldowns[0] != test.want {
				t.Errorf("cooldowns = %v, want one of %s", fake.cooldowns, test.want)
			}
		})
	}
}

func TestAProviderThatOnlyAnswersTooManyRequestsFails(t *testing.T) {
	client, fake := newFakeTMDb(t, nil)
	fake.tooMany = providerAttempts + 1

	if _, err := client.search(t.Context(), libraryKindMovies, "The Thing", 1982); err == nil {
		t.Fatal("the search reported no error, want one")
	}
	if len(fake.cooldowns) != providerAttempts-1 {
		t.Errorf("cooldowns = %d, want one less than the attempts", len(fake.cooldowns))
	}
}

func TestARefusedKeyIsAnError(t *testing.T) {
	client, fake := newFakeTMDb(t, nil)
	fake.statuses[tmdbKey("/3/search/movie", "The Thing", "1982")] = http.StatusUnauthorized

	if _, err := client.search(t.Context(), libraryKindMovies, "The Thing", 1982); err == nil {
		t.Error("the search reported no error, want one")
	}
}

func TestAnAnswerThatIsNotJSONIsAnError(t *testing.T) {
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "The Thing", "1982"): `not json`,
	})

	if _, err := client.search(t.Context(), libraryKindMovies, "The Thing", 1982); err == nil {
		t.Error("the search reported no error, want one")
	}
}

func TestAProviderThatDoesNotAnswerIsAnError(t *testing.T) {
	client := newTMDbClient("http://127.0.0.1:1", "a-token")
	client.http = &http.Client{Timeout: time.Second}

	if _, err := client.runtime(t.Context(), libraryKindMovies, 1091); err == nil {
		t.Error("the read reported no error, want one")
	}
}

func TestAWaitEndsOnTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := waitFor(ctx, time.Hour); err == nil {
		t.Error("the wait reported no error, want the context's")
	}
	if err := waitFor(t.Context(), time.Millisecond); err != nil {
		t.Errorf("the wait reported %v, want none", err)
	}
}

// A search with no year sends no year parameter at all, because a year of 0
// would match nothing.
func TestASearchWithNoYearNarrowsToNone(t *testing.T) {
	client, fake := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "Untitled", ""): `{"results":[{"id":7,"title":"Untitled"}]}`,
	})

	results, err := client.search(t.Context(), libraryKindMovies, "Untitled", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].year() != 0 {
		t.Fatalf("read %+v, want the one result with no year", results)
	}
	if fake.served[tmdbKey("/3/search/movie", "Untitled", "")] != 1 {
		t.Errorf("served %v, want one search with no year", fake.served)
	}
}

// The id a test states in a fake answer, kept beside the answers it belongs
// to so a case reads in one place.
func tmdbResultJSON(id int, title, date string) string {
	return `{"id":` + strconv.Itoa(id) + `,"title":"` + title + `","original_title":"` + title + `","release_date":"` + date + `"}`
}

// The two shapes a key takes: the v3 API key is 32 hex characters, and the v4
// read access token is anything longer.
const (
	tmdbV3APIKey         = "0123456789abcdef0123456789ABCDEF"
	tmdbV4AccessToken    = "eyJhbGciOiJIUzI1NiJ9.eyJhdWQiOiIwMTIzIn0.c2lnbmF0dXJl"
	tmdbKeyOfNoKnownForm = "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
)

// The form the key travels in comes from its shape alone. A v3 key sent as a
// bearer token is what TMDb refuses.
func TestTheIdentityClientSendsTheKeyInTheFormItsShapeNames(t *testing.T) {
	cases := []struct {
		name       string
		key        string
		wantHeader string
		wantQuery  string
	}{
		{name: "a v3 api key travels as a query parameter",
			key: tmdbV3APIKey, wantQuery: tmdbV3APIKey},
		{name: "a v4 read access token travels as a bearer token",
			key: tmdbV4AccessToken, wantHeader: "Bearer " + tmdbV4AccessToken},
		{name: "thirty-two characters that are not hex travel as a bearer token",
			key: tmdbKeyOfNoKnownForm, wantHeader: "Bearer " + tmdbKeyOfNoKnownForm},
		{name: "hex of another length travels as a bearer token",
			key: "0123456789abcdef", wantHeader: "Bearer 0123456789abcdef"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client, fake := newFakeTMDb(t, nil)
			client.key = test.key

			if _, err := client.search(t.Context(), libraryKindMovies, "The Thing", 1982); err != nil {
				t.Fatal(err)
			}

			fake.mutex.Lock()
			defer fake.mutex.Unlock()
			if fake.authorization != test.wantHeader || fake.apiKey != test.wantQuery {
				t.Errorf("the key arrived as %q and %q, want %q and %q",
					fake.authorization, fake.apiKey, test.wantHeader, test.wantQuery)
			}
		})
	}
}
