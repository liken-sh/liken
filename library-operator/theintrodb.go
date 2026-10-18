package main

// theintrodb.go is what the marks fact asks TheIntroDB: the intro, recap,
// credits, and preview spans of one movie or one episode. TheIntroDB keys a
// work on its TheTVDB id and stores each episode in TheTVDB's default order.
// It reads the file's length to choose the release version whose spans fit
// the file, such as a theatrical cut or an extended one. A key is optional.
// With one, the answer also holds the account's own pending submissions, the
// day's allowance is 100,000 asks for the account in place of 5,000 for the
// address, and the window allows 120 asks every 10 seconds in place of 30.

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The provider's own address. The API is under
// /v4, and the service's own health path is at the root.
const theintrodbAPIBase = "https://api.theintrodb.org"

// The path the fact asks, and the path the check reads. The health path
// carries no rate limit and no daily allowance, so the check spends none of
// the allowance the fact needs. It reads no key, and
// TheIntroDB answers a key it does not recognize as it answers no key, so no
// check can find a refused key.
const (
	theintrodbMediaPath = "/v4/media"
	theintrodbCheckPath = "/health"
)

// The pace of an account that names a key: 120 asks every 10 seconds, with
// room for the check. The table holds the anonymous pace.
const theintrodbKeyedPace = 90 * time.Millisecond

// One account with TheIntroDB. The token is empty for an account that names
// no Secret, and the requests then carry no header.
type theintrodbClient struct {
	providerRequests
}

func newTheIntroDBClient(base, token string, transport http.RoundTripper) *theintrodbClient {
	requests := newProviderRequests(providerBlockTheIntroDB, base, transport,
		func(request *http.Request) { authorizeTheIntroDB(request, token) })
	if token != "" {
		requests.interval = theintrodbKeyedPace
	}
	return &theintrodbClient{requests}
}

// The key travels as a bearer token, and only where the account names one.
func authorizeTheIntroDB(request *http.Request, key string) {
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
}

// TheIntroDB's answer: one list per kind, each omitted where no submission
// exists. Every list holds every candidate span, from several submissions or
// several release versions.
type theintrodbMedia struct {
	Intro   []markSpan `json:"intro"`
	Recap   []markSpan `json:"recap"`
	Credits []markSpan `json:"credits"`
	Preview []markSpan `json:"preview"`
}

// The spans of one work. A 404 is TheIntroDB saying it holds no submission
// for the work, which is an answer and not a failure.
func (c *theintrodbClient) media(ctx context.Context, query url.Values) (theintrodbMedia, bool, error) {
	var answer theintrodbMedia
	err := c.get(ctx, theintrodbMediaPath, query, &answer)
	if answeredWith(err, http.StatusNotFound) {
		return theintrodbMedia{}, false, nil
	}
	if err != nil {
		return theintrodbMedia{}, false, err
	}
	return answer, true, nil
}

// TheIntroDB's answerer: one account, asked for the spans of one file.
type theintrodbMarkAnswerer struct {
	client *theintrodbClient
}

func newTheIntroDBMarkAnswerer(client *theintrodbClient) theintrodbMarkAnswerer {
	return theintrodbMarkAnswerer{client: client}
}

func (a theintrodbMarkAnswerer) providerBlock() string { return providerBlockTheIntroDB }

// The ids an ask may name, in the order the fact tries them. TheIntroDB reads
// an episode's two numbers in the numbering of the id the ask names: TMDb's
// for a TMDb id, IMDb's for an IMDb id, and TheTVDB's default order for a
// TheTVDB id. The catalog's numbers come from the file's name or its .nfo
// file, which Sonarr writes in TheTVDB's order, so an episode names the
// series' TheTVDB id first. A movie has no numbers, and its TMDb id is the
// one a movie most often holds.
var (
	theintrodbEpisodeIDs = []theintrodbID{{"tvdb", "tvdb_id"}, {"tmdb", "tmdb_id"}, {"imdb", "imdb_id"}}
	theintrodbMovieIDs   = []theintrodbID{{"tmdb", "tmdb_id"}, {"tvdb", "tvdb_id"}, {"imdb", "imdb_id"}}
)

// One id: the provider the catalog's alias names, and the parameter
// TheIntroDB reads it from.
type theintrodbID struct {
	provider  string
	parameter string
}

// The first id the work holds, as the query that names it, or nothing for a
// work that holds none.
func theintrodbQuery(file markFile) (url.Values, bool) {
	order := theintrodbEpisodeIDs
	if file.movie {
		order = theintrodbMovieIDs
	}
	for _, id := range order {
		if value := theintrodbIDValue(id.provider, file.ids[id.provider]); value != "" {
			return url.Values{id.parameter: {value}}, true
		}
	}
	return nil, false
}

// TheTVDB and TMDb ids are positive numbers, and an IMDb id is a tt id. Any
// other value is absent.
func theintrodbIDValue(provider, value string) string {
	if provider == "imdb" {
		if strings.HasPrefix(value, "tt") {
			return value
		}
		return ""
	}
	if id, err := strconv.Atoi(value); err == nil && id > 0 {
		return strconv.Itoa(id)
	}
	return ""
}

// Every span TheIntroDB holds for the file's work, in the order intro, recap,
// credits, preview, and in TheIntroDB's own order inside each kind. A work
// with none of the three ids gets no call. An episode names the series' id
// and its two numbers, and the ask names no mapping, because the catalog does
// not record which of TheTVDB's orders a library follows. The length goes
// with the ask wherever the probe measured one.
func (a theintrodbMarkAnswerer) marks(ctx context.Context, file markFile) ([]markEntry, error) {
	query, named := theintrodbQuery(file)
	if !named {
		return nil, nil
	}
	if !file.movie {
		query.Set("season", strconv.Itoa(file.season))
		query.Set("episode", strconv.Itoa(file.episode))
	}
	if file.duration > 0 {
		query.Set("duration_ms", strconv.FormatInt(file.duration, 10))
	}
	media, held, err := a.client.media(ctx, query)
	if err != nil || !held {
		return nil, err
	}
	entries := []markEntry{}
	for _, kind := range []struct {
		name  string
		spans []markSpan
	}{
		{markKindIntro, media.Intro},
		{markKindRecap, media.Recap},
		{markKindCredits, media.Credits},
		{markKindPreview, media.Preview},
	} {
		for _, span := range kind.spans {
			entries = append(entries, span.entry(kind.name, providerBlockTheIntroDB))
		}
	}
	return entries, nil
}
