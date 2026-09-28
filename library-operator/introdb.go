package main

// introdb.go is what the marks fact asks IntroDB: the intro, recap, credits,
// and post-credits spans of one movie or one episode. IntroDB keys a work on
// its IMDb id and serves its reads with no account. It answers one span per
// kind, or null for a kind no submission covers, and it reads no file length.

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// The provider's own address, which only a test replaces.
var introdbAPIBase = "https://api.introdb.app"

// The path the fact asks, and the path the check reads.
const (
	introdbSegmentsPath = "/segments"
	introdbCheckPath    = "/health"
)

// IntroDB needs no account, so this client holds the address and nothing
// else.
type introdbClient struct {
	providerRequests
}

func newIntroDBClient(base string) *introdbClient {
	return &introdbClient{newProviderRequests(providerBlockIntroDB, base, nil)}
}

// IntroDB's answer: one span or null per kind. IntroDB calls the end credits
// the outro, and a scene after the credits the post-credits.
type introdbSegments struct {
	Intro       *markSpan `json:"intro"`
	Recap       *markSpan `json:"recap"`
	Outro       *markSpan `json:"outro"`
	PostCredits *markSpan `json:"post_credits"`
}

// IntroDB's answerer, asked for the spans of one file.
type introdbMarkAnswerer struct {
	client *introdbClient
}

func newIntroDBMarkAnswerer(client *introdbClient) introdbMarkAnswerer {
	return introdbMarkAnswerer{client: client}
}

func (a introdbMarkAnswerer) providerBlock() string { return providerBlockIntroDB }

// Every span IntroDB holds for the file's work, under this fact's kinds. A
// work with no IMDb id gets no call. An episode names the series' id and its
// two aired numbers, and a movie names the movie flag in their place. An
// answer of nulls and a 404 are both IntroDB holding nothing for the work.
func (a introdbMarkAnswerer) marks(ctx context.Context, file markFile) ([]markEntry, error) {
	id := file.ids["imdb"]
	if id == "" {
		return nil, nil
	}
	query := url.Values{"imdb_id": {id}}
	if file.movie {
		query.Set("is_movie", "true")
	} else {
		query.Set("season", strconv.Itoa(file.season))
		query.Set("episode", strconv.Itoa(file.episode))
	}
	var segments introdbSegments
	err := a.client.get(ctx, introdbSegmentsPath, query, &segments)
	if answeredWith(err, http.StatusNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries := []markEntry{}
	for _, kind := range []struct {
		name string
		span *markSpan
	}{
		{markKindIntro, segments.Intro},
		{markKindRecap, segments.Recap},
		{markKindCredits, segments.Outro},
		{markKindPostCredits, segments.PostCredits},
	} {
		if kind.span != nil {
			entries = append(entries, kind.span.entry(kind.name, providerBlockIntroDB))
		}
	}
	return entries, nil
}
