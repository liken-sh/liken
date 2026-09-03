package main

// What the nfo facts ask OMDb: one lookup by IMDb id that answers the plot,
// the US certification, and the ratings of three sites. The free tier holds a
// thousand calls a day. A call past the limit ends OMDb's work for the run,
// because no container sleeps for hours.

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// The provider's own address, which only a test replaces.
var omdbAPIBase = "https://www.omdbapi.com"

// OMDb serves one path. The parameters say which title and how much of the
// plot. The full plot is the one the nfo writes.
const (
	omdbPath      = "/"
	omdbFullPlot  = "full"
	omdbCheckPath = "/?i=tt0068646"
)

// The Source strings OMDb writes in its Ratings list. The docs page read on
// 2026-09-03 did not show them, so a lookup that finds no source is a miss
// and not an error.
const (
	omdbSourceIMDb           = "Internet Movie Database"
	omdbSourceRottenTomatoes = "Rotten Tomatoes"
	omdbSourceMetacritic     = "Metacritic"
)

// What OMDb writes when it holds no such title, and the word a 401 carries
// when the day's calls are gone.
const (
	omdbFalse       = "False"
	omdbLimitPhrase = "limit"
)

// One account with OMDb, and what its key has done so far: whether a call
// ever worked, and whether the day's limit ended its work.
type omdbClient struct {
	providerRequests
	key string

	mutex   sync.Mutex
	worked  bool
	limited bool
}

func newOMDbClient(base, key string) *omdbClient {
	client := &omdbClient{key: key}
	client.providerRequests = newProviderRequests(providerBlockOMDb, base,
		func(request *http.Request) { queryKey(omdbAPIKeyParameter, client.key)(request) })
	return client
}

// The parameter OMDb reads the key from.
const omdbAPIKeyParameter = "apikey"

// One title as OMDb answers it. Every value is a string, OMDb's own form, the
// numbers included, and a value it does not hold is N/A.
type omdbTitle struct {
	Title      string      `json:"Title"`
	Year       string      `json:"Year"`
	Rated      string      `json:"Rated"`
	Released   string      `json:"Released"`
	Runtime    string      `json:"Runtime"`
	Genre      string      `json:"Genre"`
	Director   string      `json:"Director"`
	Writer     string      `json:"Writer"`
	Actors     string      `json:"Actors"`
	Plot       string      `json:"Plot"`
	Poster     string      `json:"Poster"`
	Ratings    []omdbScore `json:"Ratings"`
	Metascore  string      `json:"Metascore"`
	IMDbRating string      `json:"imdbRating"`
	IMDbVotes  string      `json:"imdbVotes"`
	IMDbID     string      `json:"imdbID"`
	Type       string      `json:"Type"`
	Response   string      `json:"Response"`
	Error      string      `json:"Error"`
}

// One site's rating, in the site's own scale: 8.5/10, 91%, or 76/100.
type omdbScore struct {
	Source string `json:"Source"`
	Value  string `json:"Value"`
}

// Whether OMDb holds this title. It answers 200 with Response False for a
// title it does not hold, so the status alone is not a find.
func (t omdbTitle) found() bool {
	return t.Response != "" && !strings.EqualFold(t.Response, omdbFalse)
}

// The value one site scored, or an empty string where OMDb names no such
// source.
func (t omdbTitle) score(source string) string {
	for _, rating := range t.Ratings {
		if rating.Source == source {
			return rating.Value
		}
	}
	return ""
}

// The title of one IMDb id, with the full plot. This is the one call every
// nfo fact of this provider reads.
func (c *omdbClient) title(ctx context.Context, imdbID string) (*omdbTitle, error) {
	answer := &omdbTitle{}
	query := url.Values{"i": {imdbID}, "plot": {omdbFullPlot}}
	if err := c.call(ctx, query, answer); err != nil {
		return nil, err
	}
	return answer, nil
}

// Every call goes through here, so the key's two 401 answers are told apart:
// a key OMDb refuses before it ever worked, and the day's limit on a key that
// did work. The docs read on 2026-09-03 did not show the body of a limit
// answer, so a 401 that names a limit and a 401 after a call that worked both
// end the run.
func (c *omdbClient) call(ctx context.Context, query url.Values, into any) error {
	err := c.get(ctx, omdbPath, query, into)
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if err == nil {
		c.worked = true
		return nil
	}
	if !answeredWith(err, http.StatusUnauthorized) {
		return err
	}
	if c.worked || strings.Contains(strings.ToLower(err.Error()), omdbLimitPhrase) {
		c.limited = true
	}
	return err
}

// Whether this key has no calls left today. The container that reads true
// leaves the rest of its titles to the next run.
func (c *omdbClient) dailyLimitReached() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.limited
}
