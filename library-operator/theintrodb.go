package main

// theintrodb.go is what the marks fact asks TheIntroDB: the intro, recap,
// credits, and preview spans of one movie or one episode. TheIntroDB keys a
// work on its TMDb id, and it reads the file's length to choose the release
// version whose spans fit the file, such as a theatrical cut or an extended
// one. A key is optional. With one, the answer also holds the account's own
// pending submissions, and the day's allowance is 1000 asks for the account
// in place of 500 for the address.

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// The provider's own address, which only a test replaces. The API is under
// /v3, and the service's own health path is at the root.
var theintrodbAPIBase = "https://api.theintrodb.org"

// The path the fact asks, and the path the check reads. The health path
// carries no rate limit and no daily allowance, so the check spends none of
// the allowance the fact needs. It reads no key, and
// TheIntroDB answers a key it does not recognize as it answers no key, so no
// check can find a refused key.
const (
	theintrodbMediaPath = "/v3/media"
	theintrodbCheckPath = "/health"
)

// One account with TheIntroDB. The token is empty for an account that names
// no Secret, and the requests then carry no header.
type theintrodbClient struct {
	providerRequests
}

func newTheIntroDBClient(base, token string) *theintrodbClient {
	return &theintrodbClient{newProviderRequests(providerBlockTheIntroDB, base,
		func(request *http.Request) { authorizeTheIntroDB(request, token) })}
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

// Every span TheIntroDB holds for the file's work, in the order intro, recap,
// credits, preview, and in TheIntroDB's own order inside each kind. The ask
// keys on the TMDb id, which TheIntroDB calls canonical, so a work with no
// TMDb id gets no call. An episode names the series' id and its two aired
// numbers. The length goes with the ask wherever the probe measured one.
func (a theintrodbMarkAnswerer) marks(ctx context.Context, file markFile) ([]markEntry, error) {
	id, err := strconv.Atoi(file.ids["tmdb"])
	if err != nil || id <= 0 {
		return nil, nil
	}
	query := url.Values{"tmdb_id": {strconv.Itoa(id)}}
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
