package main

// tmdb.go is the whole of what the identity fact asks TMDb: a search by
// title and year, and the runtime of one result. A 429 is a cooldown inside
// the container and nothing more. TMDb's limit is about 40 requests a second,
// and one library's gaps never come near it, so no limiter lives anywhere
// else.

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// The provider's own address, which only a test replaces.
var tmdbAPIBase = "https://api.themoviedb.org"

// One account with TMDb.
type tmdbClient struct {
	providerRequests
	key string
}

func newTMDbClient(base, key string) *tmdbClient {
	client := &tmdbClient{key: key}
	client.providerRequests = newProviderRequests(providerBlockTMDb, base,
		func(request *http.Request) { authorizeTMDb(request, client.key) })
	return client
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
	// The ladder reads it because two shows of one name are told apart by the
	// country a namer writes after the title.
	OriginCountry []string `json:"origin_country"`
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

// TMDb takes two credential kinds. A v3 API key is 32 hex characters and
// travels as the api_key query parameter. Anything else is a v4 read access
// token and travels as a bearer token. TMDb refuses a v3 key sent as a bearer
// token, checked against the real API on 2026-09-03. This is the one place
// either caller decides.
const tmdbV3KeyLength = 32

func authorizeTMDb(request *http.Request, key string) {
	if _, err := hex.DecodeString(key); err == nil && len(key) == tmdbV3KeyLength {
		queryKey("api_key", key)(request)
		return
	}
	request.Header.Set("Authorization", "Bearer "+key)
}

// The cheapest authenticated read TMDb serves, so the operator's check costs
// one request and names no title.
const tmdbConfigurationPath = "/3/configuration"
