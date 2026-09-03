package main

// tmdb.go is the whole of what the identity concern asks TMDb: a search by
// title and year, and the runtime of one result. A 429 is a cooldown inside
// the container and nothing more. TMDb's limit is about 40 requests a second,
// and one library's gaps never come near it, so no limiter lives anywhere
// else.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The provider's own address, which only a test replaces.
const tmdbAPIBase = "https://api.themoviedb.org"

// The cooldown a 429 with no Retry-After header takes.
const tmdbCooldown = 10 * time.Second

// How many times one request goes out, so a provider that answers 429 without
// end fails the attempt instead of holding the container.
const tmdbAttempts = 3

// One request's bound, so a provider that stops answering cannot hold the
// container open.
var tmdbRequestTimeout = 30 * time.Second

// One account with TMDb, and the wait a cooldown takes, which a test replaces
// so no test sleeps.
type tmdbClient struct {
	base string
	key  string
	http *http.Client
	wait func(context.Context, time.Duration) error
}

func newTMDbClient(base, key string) *tmdbClient {
	return &tmdbClient{
		base: base,
		key:  key,
		http: &http.Client{Timeout: tmdbRequestTimeout},
		wait: waitFor,
	}
}

// The wait ends on the context as well as on the clock, so a container that
// is told to stop does not sleep out its cooldown first.
func waitFor(ctx context.Context, cooldown time.Duration) error {
	timer := time.NewTimer(cooldown)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// One search result, with the movie fields and the series fields together,
// because the two searches answer the same shape under two names.
type tmdbResult struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	ReleaseDate   string `json:"release_date"`
	Name          string `json:"name"`
	OriginalName  string `json:"original_name"`
	FirstAirDate  string `json:"first_air_date"`
}

// One accessor answers for both kinds: a movie states title, and a series
// states name.
func (r tmdbResult) name() string {
	if r.Title != "" {
		return r.Title
	}
	return r.Name
}

func (r tmdbResult) originalName() string {
	if r.OriginalTitle != "" {
		return r.OriginalTitle
	}
	return r.OriginalName
}

// The year comes off release_date for a movie and first_air_date for a
// series. TMDb states the first release anywhere, which is why the ladder
// also tries the years on either side.
func (r tmdbResult) year() int {
	if r.ReleaseDate != "" {
		return leadingYear(r.ReleaseDate)
	}
	return leadingYear(r.FirstAirDate)
}

type tmdbSearchAnswer struct {
	Results []tmdbResult `json:"results"`
}

// A movie states one runtime, and a series states a list of episode runtimes.
// The ladder reads the first of the list, which is the usual length of an
// episode.
type tmdbDetailAnswer struct {
	Runtime        int   `json:"runtime"`
	EpisodeRuntime []int `json:"episode_run_time"`
}

func (a tmdbDetailAnswer) runtime() time.Duration {
	minutes := a.Runtime
	if minutes == 0 && len(a.EpisodeRuntime) > 0 {
		minutes = a.EpisodeRuntime[0]
	}
	return time.Duration(minutes) * time.Minute
}

// The search is narrowed by the year the name carried, because a title alone
// matches every remake. A series takes its own year parameter,
// first_air_date_year, where a movie takes year.
func (c *tmdbClient) search(ctx context.Context, kind, title string, year int) ([]tmdbResult, error) {
	query := url.Values{"query": {title}}
	path := "/3/search/movie"
	yearField := "year"
	if kind == libraryKindSeries {
		path = "/3/search/tv"
		yearField = "first_air_date_year"
	}
	if year > 0 {
		query.Set(yearField, strconv.Itoa(year))
	}
	var answer tmdbSearchAnswer
	if err := c.get(ctx, path, query, &answer); err != nil {
		return nil, err
	}
	return answer.Results, nil
}

// The runtime is a second call, because a search result carries none. The
// ladder makes it only on the rung that needs it.
func (c *tmdbClient) runtime(ctx context.Context, kind string, id int) (time.Duration, error) {
	path := "/3/movie/" + strconv.Itoa(id)
	if kind == libraryKindSeries {
		path = "/3/tv/" + strconv.Itoa(id)
	}
	var answer tmdbDetailAnswer
	if err := c.get(ctx, path, nil, &answer); err != nil {
		return 0, err
	}
	return answer.runtime(), nil
}

// The whole retry rule: a 429 waits the header's own cooldown, or ten seconds
// where it names none, and the request goes out again.
func (c *tmdbClient) get(ctx context.Context, path string, query url.Values, into any) error {
	for attempt := 1; ; attempt++ {
		status, cooldown, body, err := c.send(ctx, path, query)
		if err != nil {
			return err
		}
		if status == http.StatusTooManyRequests && attempt < tmdbAttempts {
			if err := c.wait(ctx, cooldown); err != nil {
				return err
			}
			continue
		}
		if status < 200 || status > 299 {
			return fmt.Errorf("tmdb %s: %d: %s", path, status, strings.TrimSpace(string(body)))
		}
		return json.Unmarshal(body, into)
	}
}

// The send builds the request and lets the key's own shape decide the form it
// travels in.
func (c *tmdbClient) send(ctx context.Context, path string, query url.Values) (int, time.Duration, []byte, error) {
	address := c.base + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, 0, nil, err
	}
	request.Header.Set("Accept", "application/json")
	authorizeTMDb(request, c.key)

	response, err := c.http.Do(request)
	if err != nil {
		return 0, 0, nil, err
	}
	defer drain(response.Body)

	body, err := io.ReadAll(io.LimitReader(response.Body, tmdbAnswerLimit))
	if err != nil {
		return 0, 0, nil, err
	}
	return response.StatusCode, retryAfter(response.Header.Get("Retry-After")), body, nil
}

// TMDb takes two credential kinds. A v3 API key is 32 hex characters and
// travels as the api_key query parameter. Anything else is a v4 read access
// token and travels as a bearer token. TMDb refuses a v3 key sent as a bearer
// token, checked against the real API on 2026-09-03. This is the one place
// either caller decides.
const tmdbV3KeyLength = 32

func authorizeTMDb(request *http.Request, key string) {
	if _, err := hex.DecodeString(key); err == nil && len(key) == tmdbV3KeyLength {
		query := request.URL.Query()
		query.Set("api_key", key)
		request.URL.RawQuery = query.Encode()
		return
	}
	request.Header.Set("Authorization", "Bearer "+key)
}

// One answer's bound, so a provider that streams without end cannot grow the
// container.
const tmdbAnswerLimit = 1 << 20

// An unreadable or absent header takes the fixed cooldown.
func retryAfter(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds <= 0 {
		return tmdbCooldown
	}
	return time.Duration(seconds) * time.Second
}
