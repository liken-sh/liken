package main

// PROSE: this file is the whole of what the identity concern asks TMDb, and
// why a 429 is a cooldown in the container rather than a rate limiter anywhere.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PROSE: the provider's own address, which only a test replaces.
const tmdbAPIBase = "https://api.themoviedb.org"

// PROSE: the cooldown a 429 with no Retry-After header takes.
const tmdbCooldown = 10 * time.Second

// PROSE: how many times one request goes out, so a provider that answers 429
// forever fails the attempt rather than holding the container.
const tmdbAttempts = 3

// PROSE: bounds one request, so a provider that stops answering cannot hold
// the container open.
var tmdbRequestTimeout = 30 * time.Second

// PROSE: one account with TMDb, and the wait a cooldown takes, which a test
// replaces so no test sleeps.
type tmdbClient struct {
	base  string
	token string
	http  *http.Client
	wait  func(context.Context, time.Duration) error
}

func newTMDbClient(base, token string) *tmdbClient {
	return &tmdbClient{
		base:  base,
		token: token,
		http:  &http.Client{Timeout: tmdbRequestTimeout},
		wait:  waitFor,
	}
}

// PROSE: says why the wait ends on the context as well as on the clock.
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

// PROSE: one search result, with the movie fields and the series fields
// together, because the two searches answer the same shape under two names.
type tmdbResult struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	ReleaseDate   string `json:"release_date"`
	Name          string `json:"name"`
	OriginalName  string `json:"original_name"`
	FirstAirDate  string `json:"first_air_date"`
}

// PROSE: says why one accessor answers for both kinds.
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

// PROSE: says which date the year comes off, and that TMDb states the first
// release anywhere.
func (r tmdbResult) year() int {
	if r.ReleaseDate != "" {
		return leadingYear(r.ReleaseDate)
	}
	return leadingYear(r.FirstAirDate)
}

type tmdbSearchAnswer struct {
	Results []tmdbResult `json:"results"`
}

// PROSE: says why a movie states one runtime and a series states a list of
// them.
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

// PROSE: says why the search is narrowed by the year the name carried, and why
// a series takes its own year parameter.
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

// PROSE: says why the runtime is a second call, made only on the rung that
// needs it.
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

// PROSE: the whole retry rule: a 429 waits the header's own cooldown, or ten
// seconds where it names none, and the request goes out again.
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

// PROSE: says that the key travels as a bearer token, which is the form TMDb's
// v4 tokens take.
func (c *tmdbClient) send(ctx context.Context, path string, query url.Values) (int, time.Duration, []byte, error) {
	address := c.base + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, 0, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")

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

// PROSE: bounds one answer, so a provider that streams without end cannot grow
// the container.
const tmdbAnswerLimit = 1 << 20

// PROSE: says why an unreadable or absent header takes the fixed cooldown.
func retryAfter(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds <= 0 {
		return tmdbCooldown
	}
	return time.Duration(seconds) * time.Second
}
