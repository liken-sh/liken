package main

// What these tests read: the search one instance answers, the trailer each
// result becomes, the result whose name carries another title, and the title
// a search needs.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
)

// What one fake instance recorded: the address of every request it was asked.
type fakePeerTube struct {
	mutex    sync.Mutex
	requests []url.URL
}

// The client and the fake instance it reads, so no test reaches a real
// instance.
func newFakePeerTube(t *testing.T, body string) (*peertubeClient, *fakePeerTube) {
	t.Helper()
	fake := &fakePeerTube{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mutex.Lock()
		fake.requests = append(fake.requests, *r.URL)
		fake.mutex.Unlock()
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	client := newPeertubeClient(server.URL)
	client.http = server.Client()
	return client, fake
}

// The search asks the instance's own path for the newest videos of one query.
func TestAPeerTubeSearchAsksForTheNewestVideos(t *testing.T) {
	client, fake := newFakePeerTube(t, trailerFixture(t, "peertube-search.json"))

	videos, err := client.search(t.Context(), "Dune: Part Three")
	if err != nil {
		t.Fatal(err)
	}

	if len(videos) != 7 {
		t.Fatalf("the search read %d videos, want 7", len(videos))
	}
	if videos[0].UUID != "6e1c2d78-2f3a-4b21-9f9a-0c1d2e3f4a5b" ||
		videos[0].ShortUUID != "uCGxtW81oT5vDUc1LoZUDk" || videos[0].Language.ID != "en" {
		t.Errorf("the first video is %+v, want its ids and its language", videos[0])
	}
	if videos[6].Language.ID != "" {
		t.Errorf("the last video states the language %q, want none", videos[6].Language.ID)
	}
	asked := fake.requests[0]
	if asked.Path != "/api/v1/search/videos" {
		t.Errorf("the search asked %q, want the search path", asked.Path)
	}
	query := asked.Query()
	if query.Get("search") != "Dune: Part Three" || query.Get("count") != "20" ||
		query.Get("sort") != "-publishedAt" {
		t.Errorf("the search asked %v, want the query, the count, and the sort", query)
	}
}

// Every result whose name carries this title and year becomes one trailer. A
// result that names another year is dropped.
func TestThePeerTubeTrailerAnswererKeepsWhatTheNameMatches(t *testing.T) {
	client, _ := newFakePeerTube(t, trailerFixture(t, "peertube-search.json"))
	answerer := newPeertubeTrailerAnswerer(client)

	entries, err := answerer.trailers(t.Context(),
		trailerTitle{kind: libraryKindMovies, title: "Dune: Part Three", year: 2026})
	if err != nil {
		t.Fatal(err)
	}

	want := []trailerEntry{
		{Path: likenSelfPath, Provider: providerBlockPeerTube,
			Key:  "6e1c2d78-2f3a-4b21-9f9a-0c1d2e3f4a5b",
			Site: trailerSitePeerTube, URL: client.base + "/w/uCGxtW81oT5vDUc1LoZUDk",
			Name: "DUNE: PART THREE (2026) - IMAX Trailer [4K Ultra HD]",
			Kind: trailerKindTrailer, Language: "en", Published: "2026-07-14",
			Score: 90, Reason: "title and year match; the name says trailer"},
		{Path: likenSelfPath, Provider: providerBlockPeerTube,
			Key:  "7f2d3e89-3a4b-4c32-8a0b-1d2e3f4a5b6c",
			Site: trailerSitePeerTube, URL: client.base + "/w/vDHyuX92pU6wEVd2MpAVEl",
			Name: "DUNE: PART THREE (2026) - Official Trailer [4K Ultra HD]",
			Kind: trailerKindTrailer, Language: "en", Published: "2026-06-02",
			Score: 90, Reason: "title and year match; the name says trailer"},
		{Path: likenSelfPath, Provider: providerBlockPeerTube,
			Key:  "8a3e4f90-4b5c-4d43-9b1c-2e3f4a5b6c7d",
			Site: trailerSitePeerTube, URL: client.base + "/w/wEIzvY03qV7xFWe3NqBWFm",
			Name: "DUNE: PART THREE (2026) - Teaser Trailer [4K Ultra HD]",
			Kind: trailerKindTeaser, Language: "en", Published: "2026-02-09",
			Score: 80, Reason: "title and year match; the name says teaser"},
		{Path: likenSelfPath, Provider: providerBlockPeerTube,
			Key:  "9b4f5a01-5c6d-4e54-8c2d-3f4a5b6c7d8e",
			Site: trailerSitePeerTube, URL: client.base + "/w/xFJawZ14rW8yGXf4OrCXGn",
			Name: `DUNE: PART THREE (2026) - "Awakening" Teaser [4K Ultra HD]`,
			Kind: trailerKindTeaser, Language: "en", Published: "2025-12-20",
			Score: 80, Reason: "title and year match; the name says teaser"},
		{Path: likenSelfPath, Provider: providerBlockPeerTube,
			Key:  "1d6b7c23-7e8f-4a76-8e4f-5b6c7d8e9f01",
			Site: trailerSitePeerTube, URL: client.base + "/w/zHLcyB36tY0AIZh6QtEZIp",
			Name: `DUNE: PART THREE (2026) - "Sandworm" TV Spot [4K Ultra HD]`,
			Kind: trailerKindSpot, Language: "en", Published: "2026-08-01",
			Score: 60, Reason: "title and year match; the name says spot"},
		{Path: likenSelfPath, Provider: providerBlockPeerTube,
			Key:  "2e7c8d34-8f90-4b87-9f50-6c7d8e9f0123",
			Site: trailerSitePeerTube,
			URL:  client.base + "/w/2e7c8d34-8f90-4b87-9f50-6c7d8e9f0123",
			Name: "DUNE: PART THREE (2026) Trailer Song",
			Kind: trailerKindOther, Published: "2026-07-19",
			Score: 40, Reason: "title and year match; the name says other"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("the answerer held\n%+v\nwant\n%+v", entries, want)
	}
	if got := answerer.providerBlock(); got != providerBlockPeerTube {
		t.Errorf("the answerer names the block %q, want %q", got, providerBlockPeerTube)
	}
}

// A title with no name is no trailer and no search.
func TestThePeerTubeTrailerAnswererHoldsNothingWithoutATitle(t *testing.T) {
	client, fake := newFakePeerTube(t, trailerFixture(t, "peertube-search.json"))
	answerer := newPeertubeTrailerAnswerer(client)

	entries, err := answerer.trailers(t.Context(), trailerTitle{kind: libraryKindMovies, year: 2026})

	if err != nil || len(entries) != 0 {
		t.Errorf("the answerer held %+v and %v, want no trailer", entries, err)
	}
	if len(fake.requests) != 0 {
		t.Errorf("the answerer made %v, want no search", fake.requests)
	}
}
