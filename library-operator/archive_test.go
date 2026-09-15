package main

// What these tests read: the search the client sends, the pace it holds to,
// the shapes a field arrives in, and the trailer each item of the collection
// becomes.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"
)

// What one fake archive recorded: the address of every request it was asked.
type fakeArchive struct {
	mutex    sync.Mutex
	requests []url.URL
}

func (f *fakeArchive) read() []url.URL {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.requests
}

// The client and the fake archive it reads, at no pace at all, so no test
// reaches archive.org and no test sleeps unless it says so.
func newFakeArchive(t *testing.T, body string) (*archiveClient, *fakeArchive) {
	t.Helper()
	fake := &fakeArchive{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mutex.Lock()
		fake.requests = append(fake.requests, *r.URL)
		fake.mutex.Unlock()
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	client := newArchiveClient(server.URL)
	client.http = server.Client()
	client.interval = 0
	return client, fake
}

// The search asks for the movie_trailers collection and this title alone. The
// title travels in quotes, so its words are one phrase.
func TestAnArchiveSearchAsksTheCollectionForOneTitle(t *testing.T) {
	client, fake := newFakeArchive(t, trailerFixture(t, "archive-search.json"))

	docs, err := client.search(t.Context(), `Don't Look Now`)
	if err != nil {
		t.Fatal(err)
	}

	asked := fake.read()[0]
	if asked.Path != archiveSearchPath {
		t.Errorf("the search asked %q, want %q", asked.Path, archiveSearchPath)
	}
	query := asked.Query()
	if got := query.Get("q"); got != `collection:movie_trailers AND title:("Don't Look Now")` {
		t.Errorf("the search asked for %q, want the collection and the quoted title", got)
	}
	if query.Get("rows") != "20" || query.Get("output") != "json" {
		t.Errorf("the search asked %v, want twenty rows of JSON", query)
	}
	if query.Get("fl[]") != archiveSearchFields {
		t.Errorf("the search asked for the fields %q, want %q", query.Get("fl[]"), archiveSearchFields)
	}
	if len(docs) != 4 {
		t.Fatalf("the search read %d items, want 4", len(docs))
	}
}

// The year arrives as a string, as a number, or not at all, and a title
// arrives as a word or as a list of them. Every one reads as one word.
func TestAnArchiveItemReadsTheShapesItsFieldsArriveIn(t *testing.T) {
	client, _ := newFakeArchive(t, trailerFixture(t, "archive-search.json"))

	docs, err := client.search(t.Context(), "His Girl Friday")
	if err != nil {
		t.Fatal(err)
	}

	want := []archiveDoc{
		{Identifier: "turner_video_71", Title: "His Girl Friday", Year: "1940",
			MediaType: "movies"},
		{Identifier: "His_Girl_Friday_trailer", Title: "His Girl Friday trailer",
			Year: "1940", Date: "1940-01-11T00:00:00Z",
			LicenseURL: "http://creativecommons.org/licenses/publicdomain/",
			MediaType:  "movies"},
		{Identifier: "HowardHawkshisGirlFridayMovieTrailer1940",
			Title: `Howard Hawks' "HIS GIRL FRIDAY" movie trailer (1940)`,
			Date:  "2013-07-22T16:39:55Z", MediaType: "movies"},
		{Identifier: "TheOthers2001TheatricalTrailer",
			Title: "The Others (2001) theatrical trailer", Year: "2001",
			Date:       "2001-06-29T00:00:00Z",
			LicenseURL: "http://creativecommons.org/licenses/publicdomain/",
			MediaType:  "movies"},
	}
	if !reflect.DeepEqual(docs, want) {
		t.Errorf("the search read\n%+v\nwant\n%+v", docs, want)
	}
}

// The client sends one request per interval, because archive.org serves its
// search to anyone and asks for one request a second.
func TestAnArchiveClientHoldsToItsOwnPace(t *testing.T) {
	cases := []struct {
		name     string
		interval time.Duration
		least    time.Duration
		most     time.Duration
	}{
		{name: "a client with no pace sends both at once",
			interval: 0, most: 50 * time.Millisecond},
		{name: "a client with a pace waits the interval out",
			interval: 50 * time.Millisecond, least: 50 * time.Millisecond,
			most: time.Minute},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			client, _ := newFakeArchive(t, trailerFixture(t, "archive-search.json"))
			client.interval = one.interval
			started := time.Now()

			_, first := client.search(t.Context(), "His Girl Friday")
			_, second := client.search(t.Context(), "His Girl Friday")

			took := time.Since(started)
			if first != nil || second != nil {
				t.Fatalf("the searches read %v and %v", first, second)
			}
			if took < one.least || took > one.most {
				t.Errorf("two searches took %v, want between %v and %v", took, one.least, one.most)
			}
		})
	}
}

// The client the operator builds paces at the interval the shared table
// states for the archive block.
func TestAnArchiveClientTakesThePaceTheTableStates(t *testing.T) {
	paceFromTheTable(t)

	client := newArchiveClient(archiveAPIBase)

	if client.interval != 250*time.Millisecond {
		t.Errorf("the client paces at %v, want four requests a second", client.interval)
	}
}

// The year an item states, out of the year field or out of its own title.
func TestTheYearAnArchiveItemStates(t *testing.T) {
	cases := []struct {
		name string
		doc  archiveDoc
		want int
	}{
		{name: "the year field", doc: archiveDoc{Year: "1940"}, want: 1940},
		{name: "a year in parentheses", want: 1940,
			doc: archiveDoc{Title: `Howard Hawks' "HIS GIRL FRIDAY" movie trailer (1940)`}},
		{name: "a year between bars", want: 1940,
			doc: archiveDoc{Title: "His Girl Friday |1940| theatrical trailer"}},
		{name: "no year at all", doc: archiveDoc{Title: "His Girl Friday trailer"}},
		{name: "a year field of no year", doc: archiveDoc{Year: "n/a"}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := archiveDocYear(one.doc); got != one.want {
				t.Errorf("the year is %d, want %d", got, one.want)
			}
		})
	}
}

// The title alone, out of the name the uploader wrote, which is the quoted
// segment where the name holds one.
func TestTheTitleAnArchiveItemStates(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{name: "His Girl Friday", want: "hisgirlfriday"},
		{name: "His Girl Friday trailer", want: "hisgirlfriday"},
		{name: "HIS GIRL FRIDAY (1940) - Official Trailer", want: "hisgirlfriday"},
		{name: "His Girl Friday [HD 1080p restoration]", want: "hisgirlfriday"},
		{name: "His Girl Friday | Columbia Pictures", want: "hisgirlfriday"},
		{name: "His Girl Friday theatrical movie trailer 4k webrip", want: "hisgirlfriday"},
		{name: `Howard Hawks' "HIS GIRL FRIDAY" movie trailer (1940)`,
			want: "hisgirlfriday"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := foldTitle(archiveDocTitle(one.name)); got != one.want {
				t.Errorf("the title folds to %q, want %q", got, one.want)
			}
		})
	}
}

// The kind an item's own name states, and trailer for a name that states
// none.
func TestTheKindAnArchiveItemStates(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{name: "His Girl Friday", want: trailerKindTrailer},
		{name: "His Girl Friday trailer", want: trailerKindTrailer},
		{name: "HIS GIRL FRIDAY (1940) teaser", want: trailerKindTeaser},
		{name: "HIS GIRL FRIDAY (1940) TV spot", want: trailerKindSpot},
		{name: "HIS GIRL FRIDAY opening clip", want: trailerKindClip},
		{name: "HIS GIRL FRIDAY trailer song", want: trailerKindOther},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := archiveDocKind(one.name); got != one.want {
				t.Errorf("the kind is %q, want %q", got, one.want)
			}
		})
	}
}

// What an item says about the title it belongs to: the title and the year its
// own name and fields state.
func TestWhatAnArchiveItemSaysAboutOneTitle(t *testing.T) {
	cases := []struct {
		name string
		doc  archiveDoc
		want trailerMatch
	}{
		{name: "the title and the year", want: trailerMatch{title: true, year: true, yearKnown: true},
			doc: archiveDoc{Title: "His Girl Friday trailer", Year: "1940"}},
		{name: "the title and no year at all", want: trailerMatch{title: true},
			doc: archiveDoc{Title: "His Girl Friday trailer"}},
		{name: "the title and another year", want: trailerMatch{title: true, yearKnown: true},
			doc: archiveDoc{Title: "His Girl Friday trailer", Year: "1939"}},
		{name: "another title", want: trailerMatch{year: true, yearKnown: true},
			doc: archiveDoc{Title: "The Others theatrical trailer", Year: "1940"}},
		{name: "a quoted title beside the name of the director",
			want: trailerMatch{title: true, year: true, yearKnown: true},
			doc:  archiveDoc{Title: `Howard Hawks' "HIS GIRL FRIDAY" movie trailer (1940)`}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			title := trailerTitle{kind: libraryKindMovies, title: "His Girl Friday", year: 1940}

			if got := archiveDocMatch(one.doc, title); got != one.want {
				t.Errorf("the match is %+v, want %+v", got, one.want)
			}
		})
	}
}

// Every item whose own title carries this title becomes one trailer. An item
// that carries another title is dropped. An item that names no kind is a
// trailer, because the collection holds trailers alone.
func TestTheArchiveTrailerAnswererKeepsWhatTheTitleMatches(t *testing.T) {
	client, _ := newFakeArchive(t, trailerFixture(t, "archive-search.json"))
	answerer := newArchiveTrailerAnswerer(client)

	entries, err := answerer.trailers(t.Context(),
		trailerTitle{kind: libraryKindMovies, title: "His Girl Friday", year: 1940})
	if err != nil {
		t.Fatal(err)
	}

	want := []trailerEntry{
		{Path: likenSelfPath, Provider: providerBlockArchive, Key: "turner_video_71",
			Site: trailerSiteArchive, URL: "https://archive.org/details/turner_video_71",
			Name: "His Girl Friday", Kind: trailerKindTrailer, Published: "1940-01-01",
			Score: 85, Reason: "title and year match; trailer; no language"},
		{Path: likenSelfPath, Provider: providerBlockArchive, Key: "His_Girl_Friday_trailer",
			Site: trailerSiteArchive, URL: "https://archive.org/details/His_Girl_Friday_trailer",
			Name: "His Girl Friday trailer", Kind: trailerKindTrailer, Published: "1940-01-11",
			Score: 85, Reason: "title and year match; trailer; no language"},
		{Path: likenSelfPath, Provider: providerBlockArchive,
			Key:  "HowardHawkshisGirlFridayMovieTrailer1940",
			Site: trailerSiteArchive,
			URL:  "https://archive.org/details/HowardHawkshisGirlFridayMovieTrailer1940",
			Name: `Howard Hawks' "HIS GIRL FRIDAY" movie trailer (1940)`,
			Kind: trailerKindTrailer, Published: "2013-07-22",
			Score: 85, Reason: "title and year match; trailer; no language"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("the answerer held\n%+v\nwant\n%+v", entries, want)
	}
	if got := answerer.providerBlock(); got != providerBlockArchive {
		t.Errorf("the answerer names the block %q, want %q", got, providerBlockArchive)
	}
}

// A field of a shape this operator has no word for reads as nothing, and an
// item that states no date and no year is published on no day.
func TestAnArchiveItemOfShapesTheOperatorHasNoWordFor(t *testing.T) {
	client, _ := newFakeArchive(t, `{"response":{"numFound":1,"docs":[`+
		`{"identifier":"odd","title":[],"year":true,"date":null,`+
		`"licenseurl":["first","second"]}]}}`)

	docs, err := client.search(t.Context(), "Odd")
	if err != nil {
		t.Fatal(err)
	}

	want := []archiveDoc{{Identifier: "odd", LicenseURL: "first"}}
	if !reflect.DeepEqual(docs, want) {
		t.Errorf("the search read %+v, want %+v", docs, want)
	}
	if got := archiveDocPublished(docs[0]); got != "" {
		t.Errorf("the item is published on %q, want no day at all", got)
	}
}

// An item that is no object at all ends the search, and an answer outside 2xx
// ends the ask.
func TestAnArchiveSearchThatCannotBeRead(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "an item that is no object", status: http.StatusOK,
			body: `{"response":{"numFound":1,"docs":["not an item"]}}`},
		{name: "an answer the archive refused",
			status: http.StatusInternalServerError, body: "the search is down"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(one.status)
					_, _ = io.WriteString(w, one.body)
				}))
			t.Cleanup(server.Close)
			client := newArchiveClient(server.URL)
			client.http = server.Client()
			client.interval = 0

			_, err := newArchiveTrailerAnswerer(client).trailers(t.Context(),
				trailerTitle{kind: libraryKindMovies, title: "His Girl Friday", year: 1940})

			if err == nil {
				t.Error("the answerer read the search, want the error the archive gave")
			}
		})
	}
}

// The wait between two requests ends on the context, so a container that is
// told to stop does not sleep its pace out first.
func TestAnArchiveSearchWaitsNoLongerThanItsContext(t *testing.T) {
	client, fake := newFakeArchive(t, trailerFixture(t, "archive-search.json"))
	client.interval = time.Minute
	ctx, stop := context.WithCancel(t.Context())

	if _, err := client.search(ctx, "His Girl Friday"); err != nil {
		t.Fatal(err)
	}
	stop()
	_, err := client.search(ctx, "His Girl Friday")

	if !errors.Is(err, context.Canceled) {
		t.Errorf("the second search read %v, want the context's own error", err)
	}
	if len(fake.read()) != 1 {
		t.Errorf("the client made %v, want the one request that went before the wait", fake.read())
	}
}

// A title with no name is no trailer and no search.
func TestTheArchiveTrailerAnswererHoldsNothingWithoutATitle(t *testing.T) {
	client, fake := newFakeArchive(t, trailerFixture(t, "archive-search.json"))
	answerer := newArchiveTrailerAnswerer(client)

	entries, err := answerer.trailers(t.Context(), trailerTitle{kind: libraryKindMovies, year: 1940})

	if err != nil || len(entries) != 0 {
		t.Errorf("the answerer held %+v and %v, want no trailer", entries, err)
	}
	if len(fake.read()) != 0 {
		t.Errorf("the answerer made %v, want no search", fake.read())
	}
}
