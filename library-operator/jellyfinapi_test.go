package main

// What these tests prove. The four calls the role makes reach the paths
// and carry the header Jellyfin reads the API key from. A listing is read
// one page at a time, and every item reaches the caller once. An answer
// this client cannot decode to its end is an error, not a short page. An
// answer outside 2xx is an error. The tick math converts to the store's
// seconds.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// One user-data write, as the fake server read it.
type fakeJellyfinWrite struct {
	item string
	user string
	data jellyfinUserData
}

// A Jellyfin that answers the four endpoints from fixture values and
// records what it was asked. Every test drives the role against one of
// these, so no test reaches a live server.
type fakeJellyfin struct {
	mutex  sync.Mutex
	users  []jellyfinUser
	items  []jellyfinItem
	byID   map[string]jellyfinItem
	status int
	// The one listing this server refuses, named by the item types in its
	// query. A test uses it to drive a build that reads one listing and
	// fails on the other.
	refuse string
	// Whether this server omits the count from a listing answer. A test
	// uses it to prove that a short page ends the read.
	countless bool
	// The largest page this server answers, whatever the query asks for.
	// A test uses it to make a server that answers a smaller page than
	// the query asked for.
	capPage        int
	listings       int
	userReads      int
	seriesReads    int
	writes         []fakeJellyfinWrite
	authorizations []string
	queries        []string
}

func (f *fakeJellyfin) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.authorizations = append(f.authorizations, request.Header.Get("Authorization"))
	f.queries = append(f.queries, request.URL.RequestURI())
	if f.status != 0 {
		http.Error(w, "the server refused", f.status)
		return
	}
	switch {
	case request.URL.Path == "/Users":
		f.userReads++
		_ = json.NewEncoder(w).Encode(f.users)
	case request.URL.Path == "/Items" && request.URL.Query().Get("ids") != "":
		f.seriesReads++
		item, held := f.byID[request.URL.Query().Get("ids")]
		if !held {
			_ = json.NewEncoder(w).Encode(jellyfinItemList{})
			return
		}
		_ = json.NewEncoder(w).Encode(jellyfinItemList{Items: []jellyfinItem{item}})
	case request.URL.Path == "/Items":
		f.listings++
		f.page(w, request)
	case strings.HasPrefix(request.URL.Path, "/UserItems/"):
		data := jellyfinUserData{}
		_ = json.NewDecoder(request.Body).Decode(&data)
		f.writes = append(f.writes, fakeJellyfinWrite{
			item: strings.Split(strings.TrimPrefix(request.URL.Path, "/UserItems/"), "/")[0],
			user: request.URL.Query().Get("userId"),
			data: data,
		})
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "no such path", http.StatusNotFound)
	}
}

// One listing answer in Jellyfin's shape: the items whose type the query
// names, cut to the page the query asks for, and the count of every item
// the query matched.
func (f *fakeJellyfin) page(w http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	if f.refuse != "" && query.Get("includeItemTypes") == f.refuse {
		http.Error(w, "the server refused", http.StatusInternalServerError)
		return
	}
	types := strings.Split(query.Get("includeItemTypes"), ",")
	matched := []jellyfinItem{}
	for _, item := range f.items {
		if slices.Contains(types, item.Type) {
			matched = append(matched, item)
		}
	}

	start, _ := strconv.Atoi(query.Get("startIndex"))
	limit, _ := strconv.Atoi(query.Get("limit"))
	if f.capPage > 0 && limit > f.capPage {
		limit = f.capPage
	}
	page := []jellyfinItem{}
	if start < len(matched) {
		end := min(start+limit, len(matched))
		page = matched[start:end]
	}
	total := len(matched)
	if f.countless {
		total = 0
	}
	_ = json.NewEncoder(w).Encode(jellyfinItemList{Items: page, Total: total})
}

// How many listing requests one build of the index makes: the series
// listing and the works listing. Every fixture here holds fewer items
// than one page, so each listing is one request.
const jellyfinFixtureBuild = 2

// The house this every test works from: two users, two films, a series,
// and one episode of it.
func jellyfinFixture() *fakeJellyfin {
	return &fakeJellyfin{
		users: []jellyfinUser{{Name: "Chris", ID: "user-chris"}, {Name: "kelly", ID: "user-kelly"}},
		items: []jellyfinItem{
			{ID: "item-matrix", Type: "Movie",
				ProviderIds: map[string]string{"Tmdb": "603", "Imdb": "tt0133093", "Tvdb": ""}},
			{ID: "item-arrival", Type: "Movie", ProviderIds: map[string]string{"Tmdb": "329865"}},
			{ID: "item-office", Type: "Series",
				ProviderIds: map[string]string{"Tmdb": "2316", "Tvdb": "73244"}},
			{ID: "item-office-3-5", Type: "Episode", SeriesID: "item-office",
				ParentIndexNumber: 3, IndexNumber: 5, ProviderIds: map[string]string{"Tmdb": "999"}},
		},
		byID: map[string]jellyfinItem{
			"item-office": {ID: "item-office", Type: "Series",
				ProviderIds: map[string]string{"Tmdb": "2316"}},
		},
	}
}

// Stands one fake Jellyfin and hands back a client that reaches it.
func standJellyfinServer(t *testing.T, fake *fakeJellyfin) *jellyfinAPI {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return newJellyfinAPI(server.URL+"/", "the-key", server.Client())
}

// A clock a test moves, so a refresh floor is proven with no sleep.
type jellyfinClock struct {
	mutex sync.Mutex
	at    time.Time
}

func newJellyfinClock() *jellyfinClock {
	return &jellyfinClock{at: time.Date(2026, 9, 8, 20, 4, 5, 0, time.UTC)}
}

func (c *jellyfinClock) now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.at
}

func (c *jellyfinClock) advance(by time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.at = c.at.Add(by)
}

// Every request names the key, this client, and this device, in the
// form Jellyfin's own clients send.
func TestEveryJellyfinRequestNamesTheKeyAndTheClient(t *testing.T) {
	fake := jellyfinFixture()
	api := standJellyfinServer(t, fake)

	if _, err := api.users(t.Context()); err != nil {
		t.Fatalf("reading the users: %v", err)
	}

	want := `MediaBrowser Token="the-key", Client="liken", Device="library-operator", DeviceId="library-operator", Version="1"`
	if got := fake.authorizations[0]; got != want {
		t.Errorf("authorization = %q, want %q", got, want)
	}
}

// The user list is the map from a Person name to the id every write
// names.
func TestTheClientReadsTheUsers(t *testing.T) {
	api := standJellyfinServer(t, jellyfinFixture())

	users, err := api.users(t.Context())

	if err != nil {
		t.Fatalf("reading the users: %v", err)
	}
	if len(users) != 2 || users[0].Name != "Chris" || users[0].ID != "user-chris" {
		t.Errorf("users = %+v, want the two the server holds", users)
	}
}

// The works listing carries every movie and episode with the provider
// ids, because Jellyfin has no lookup by provider id. It asks for no
// images, because the role reads provider ids alone.
func TestTheClientReadsTheWorksListing(t *testing.T) {
	fake := jellyfinFixture()
	api := standJellyfinServer(t, fake)

	held, err := jellyfinHeld(t, api, jellyfinWorksListing)

	if err != nil {
		t.Fatalf("reading the items: %v", err)
	}
	if len(held) != 3 || held[2].SeriesID != "item-office" || held[2].IndexNumber != 5 {
		t.Errorf("items = %+v, want the two films and the episode", held)
	}
	want := "/Items?recursive=true&includeItemTypes=Movie,Episode&fields=ProviderIds" +
		"&enableImages=false&startIndex=0&limit=500"
	if fake.queries[0] != want {
		t.Errorf("query = %q, want %q", fake.queries[0], want)
	}
}

// The series listing is read apart from the works, because an episode is
// keyed on its series' provider ids and only the series carries them.
func TestTheClientReadsTheSeriesListing(t *testing.T) {
	api := standJellyfinServer(t, jellyfinFixture())

	held, err := jellyfinHeld(t, api, jellyfinSeriesListing)

	if err != nil {
		t.Fatalf("reading the series: %v", err)
	}
	if len(held) != 1 || held[0].ID != "item-office" {
		t.Errorf("items = %+v, want the one series", held)
	}
}

// A listing larger than one page is read one page at a time, and every
// item reaches the caller once. The page size bounds the memory a listing
// takes, so the role's peak memory does not grow with the library.
func TestTheClientReadsALargeListingOnePageAtATime(t *testing.T) {
	fake := jellyfinFixture()
	fake.items = []jellyfinItem{
		{ID: "item-1", Type: jellyfinMovieType}, {ID: "item-2", Type: jellyfinMovieType},
		{ID: "item-3", Type: jellyfinMovieType}, {ID: "item-4", Type: jellyfinMovieType},
		{ID: "item-5", Type: jellyfinMovieType},
	}
	api := standJellyfinServer(t, fake)
	jellyfinPagesOf(t, 2)

	held, err := jellyfinHeld(t, api, jellyfinWorksListing)

	if err != nil {
		t.Fatalf("reading the items: %v", err)
	}
	if got := jellyfinIDs(held); !slices.Equal(got, []string{"item-1", "item-2", "item-3", "item-4", "item-5"}) {
		t.Errorf("items = %v, want the five the server holds, each once", got)
	}
	if fake.listings != 3 {
		t.Errorf("the client made %d requests, want three pages of two", fake.listings)
	}
	if !strings.Contains(fake.queries[1], "startIndex=2") || !strings.Contains(fake.queries[2], "startIndex=4") {
		t.Errorf("queries = %v, want each page to name where it starts", fake.queries)
	}
}

// The count the server states ends a read, so a listing whose last page
// is full costs no extra request.
func TestTheCountEndsAReadWhoseLastPageIsFull(t *testing.T) {
	fake := jellyfinFixture()
	fake.items = []jellyfinItem{
		{ID: "item-1", Type: jellyfinMovieType}, {ID: "item-2", Type: jellyfinMovieType},
		{ID: "item-3", Type: jellyfinMovieType}, {ID: "item-4", Type: jellyfinMovieType},
	}
	api := standJellyfinServer(t, fake)
	jellyfinPagesOf(t, 2)

	held, err := jellyfinHeld(t, api, jellyfinWorksListing)

	if err != nil {
		t.Fatalf("reading the items: %v", err)
	}
	if len(held) != 4 {
		t.Errorf("items = %v, want the four the server holds", jellyfinIDs(held))
	}
	if fake.listings != 2 {
		t.Errorf("the client made %d requests, want the two pages the count names", fake.listings)
	}
}

// A server that answers a smaller page than it was asked for is read to
// its end, because the count it states names more items than the pages
// have delivered. A read that ended at the first short page would build
// the index from part of the library.
func TestAServerThatAnswersASmallPageIsReadThrough(t *testing.T) {
	fake := jellyfinFixture()
	fake.capPage = 2
	fake.items = []jellyfinItem{
		{ID: "item-1", Type: jellyfinMovieType}, {ID: "item-2", Type: jellyfinMovieType},
		{ID: "item-3", Type: jellyfinMovieType}, {ID: "item-4", Type: jellyfinMovieType},
		{ID: "item-5", Type: jellyfinMovieType},
	}
	api := standJellyfinServer(t, fake)

	held, err := jellyfinHeld(t, api, jellyfinWorksListing)

	if err != nil {
		t.Fatalf("reading the items: %v", err)
	}
	if got := jellyfinIDs(held); !slices.Equal(got,
		[]string{"item-1", "item-2", "item-3", "item-4", "item-5"}) {
		t.Errorf("items = %v, want the five the server holds", got)
	}
}

// A server that states no count is read until its first short page, so
// the role builds its whole index from a server that omits the count.
func TestAServerThatStatesNoCountIsReadToItsShortPage(t *testing.T) {
	fake := jellyfinFixture()
	fake.countless = true
	fake.items = []jellyfinItem{
		{ID: "item-1", Type: jellyfinMovieType}, {ID: "item-2", Type: jellyfinMovieType},
		{ID: "item-3", Type: jellyfinMovieType},
	}
	api := standJellyfinServer(t, fake)
	jellyfinPagesOf(t, 2)

	held, err := jellyfinHeld(t, api, jellyfinWorksListing)

	if err != nil {
		t.Fatalf("reading the items: %v", err)
	}
	if got := jellyfinIDs(held); !slices.Equal(got, []string{"item-1", "item-2", "item-3"}) {
		t.Errorf("items = %v, want the three the server holds", got)
	}
	if fake.listings != 2 {
		t.Errorf("the client made %d requests, want a full page and a short one", fake.listings)
	}
}

// An answer past jellyfinAnswerLimit fails the read instead of filling the
// pod's memory. The limit is far below the pod's memory limit. So a server
// that answers more than it was asked for costs the role one index build,
// and never the container.
func TestAnAnswerPastTheCeilingIsAnError(t *testing.T) {
	fake := jellyfinFixture()
	api := standJellyfinServer(t, fake)
	held := jellyfinAnswerLimit
	jellyfinAnswerLimit = 16
	t.Cleanup(func() { jellyfinAnswerLimit = held })

	_, err := jellyfinHeld(t, api, jellyfinWorksListing)

	if err == nil {
		t.Fatal("the client read an answer past the ceiling")
	}
}

// A listing answer this client cannot decode to its end is an error,
// never a short page. A short page would end the read, and the build
// would install an index that lacks every work in the rest of the
// answer.
func TestAListingAnswerThisClientCannotReadIsAnError(t *testing.T) {
	for _, test := range []struct {
		name   string
		answer string
	}{
		{name: "an answer that is no object", answer: `[]`},
		{name: "an answer that stops after a field name", answer: `{"Items"`},
		{name: "an answer that stops inside an item", answer: `{"Items":[{"Id":"item-1"`},
		{name: "an answer that stops after the items", answer: `{"Items":[]`},
		{name: "an item of another shape", answer: `{"Items":[3]}`},
		{name: "a count of another shape", answer: `{"TotalRecordCount":"many"}`},
		{name: "a field that stops inside its value", answer: `{"StartIndex":`},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := standJellyfinAnswer(t, test.answer)

			_, err := jellyfinHeld(t, api, jellyfinWorksListing)

			if err == nil {
				t.Error("the client read an answer it cannot read through")
			}
		})
	}
}

// An answer with no items reads as an empty page, which ends the read. A
// server answers this for a listing with no items, and the build installs
// an empty index.
func TestAListingThatCarriesNoItemsReadsAsAnEmptyPage(t *testing.T) {
	for _, test := range []struct {
		name   string
		answer string
	}{
		{name: "no items field", answer: `{"TotalRecordCount":0}`},
		{name: "an items field of nothing", answer: `{"Items":null,"TotalRecordCount":0}`},
		{name: "an empty items field", answer: `{"Items":[],"TotalRecordCount":0}`},
		{name: "a field this client does not read", answer: `{"Items":[],"StartIndex":0}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := standJellyfinAnswer(t, test.answer)

			held, err := jellyfinHeld(t, api, jellyfinWorksListing)

			if err != nil {
				t.Fatalf("reading the items: %v", err)
			}
			if len(held) != 0 {
				t.Errorf("items = %+v, want none", held)
			}
		})
	}
}

// Starts a server that answers every listing with one body the test
// wrote, for an answer the fixture server cannot produce.
func standJellyfinAnswer(t *testing.T, answer string) *jellyfinAPI {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", jsonContentType)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(server.Close)
	return newJellyfinAPI(server.URL, "the-key", server.Client())
}

// Reads one whole listing into a slice, which is what a test asserts on
// and what the role itself never holds.
func jellyfinHeld(t *testing.T, api *jellyfinAPI, listing string) ([]jellyfinItem, error) {
	t.Helper()
	held := []jellyfinItem{}
	err := api.items(t.Context(), listing, func(item jellyfinItem) {
		held = append(held, item)
	})
	return held, err
}

func jellyfinIDs(items []jellyfinItem) []string {
	ids := []string{}
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

// Sets the listing page size to one a test can count, and restores it
// afterwards.
func jellyfinPagesOf(t *testing.T, size int) {
	t.Helper()
	held := jellyfinPageSize
	jellyfinPageSize = size
	t.Cleanup(func() { jellyfinPageSize = held })
}

// One item by id is how the role reads a series an episode names.
func TestTheClientReadsOneItemByID(t *testing.T) {
	fake := jellyfinFixture()
	api := standJellyfinServer(t, fake)

	item, err := api.item(t.Context(), "item-office")

	if err != nil {
		t.Fatalf("reading the series: %v", err)
	}
	if item.ProviderIds["Tmdb"] != "2316" {
		t.Errorf("item = %+v, want the series' provider ids", item)
	}
	if want := "/Items?fields=ProviderIds&enableImages=false&ids=item-office"; fake.queries[0] != want {
		t.Errorf("query = %q, want %q", fake.queries[0], want)
	}
}

// The write names the item in the path and the user in the query, which
// is what lets one administrator key write any person's progress.
func TestTheClientWritesOnePersonsUserData(t *testing.T) {
	fake := jellyfinFixture()
	api := standJellyfinServer(t, fake)
	data := jellyfinUserData{PlaybackPositionTicks: 42100000000, Played: true, LastPlayedDate: "2026-09-08T20:04:05Z"}

	err := api.writeUserData(t.Context(), "item-matrix", "user-chris", data)

	if err != nil {
		t.Fatalf("writing the user data: %v", err)
	}
	if len(fake.writes) != 1 || fake.writes[0].item != "item-matrix" || fake.writes[0].user != "user-chris" {
		t.Fatalf("writes = %+v, want one write of the film for chris", fake.writes)
	}
	if fake.writes[0].data != data {
		t.Errorf("data = %+v, want %+v", fake.writes[0].data, data)
	}
}

// An answer outside 2xx is an error that names the path and the status,
// and it carries no key, because the key travels in a header.
func TestAnAnswerOutsideSuccessIsAnError(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
	}{
		{name: "a key the server refused", status: http.StatusUnauthorized},
		{name: "a path the server does not hold", status: http.StatusNotFound},
		{name: "a server that failed", status: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := jellyfinFixture()
			fake.status = test.status
			api := standJellyfinServer(t, fake)

			_, err := api.users(t.Context())

			if err == nil {
				t.Fatal("the client read an answer the server refused")
			}
			if !strings.Contains(err.Error(), "/Users") || strings.Contains(err.Error(), "the-key") {
				t.Errorf("error = %q, want the path and no key", err)
			}
		})
	}
}

// A write the server refused is an error the caller logs.
func TestAWriteTheServerRefusedIsAnError(t *testing.T) {
	fake := jellyfinFixture()
	fake.status = http.StatusForbidden
	api := standJellyfinServer(t, fake)

	err := api.writeUserData(t.Context(), "item-matrix", "user-chris", jellyfinUserData{})

	if err == nil {
		t.Fatal("the client wrote to a server that refused it")
	}
}

// A server that is not there is an error and not a panic.
func TestAServerThatIsNotThereIsAnError(t *testing.T) {
	api := newJellyfinAPI("http://127.0.0.1:1", "the-key", &http.Client{Timeout: time.Second})

	err := api.items(context.Background(), jellyfinWorksListing, func(jellyfinItem) {})

	if err == nil {
		t.Fatal("the client read a server that is not there")
	}
}

// Ten million ticks are one second, which is the shape the store holds.
func TestSecondsBecomeTicks(t *testing.T) {
	for _, test := range []struct {
		name    string
		seconds int
		ticks   int64
	}{
		{name: "the start", seconds: 0, ticks: 0},
		{name: "one second", seconds: 1, ticks: 10_000_000},
		{name: "an hour and a half", seconds: 5400, ticks: 54_000_000_000},
		{name: "a position below zero", seconds: -5, ticks: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := jellyfinTicks(test.seconds); got != test.ticks {
				t.Errorf("ticks of %d = %d, want %d", test.seconds, got, test.ticks)
			}
		})
	}
}

// The read rounds down, so a position never reads past where the person
// reached.
func TestTicksBecomeSeconds(t *testing.T) {
	for _, test := range []struct {
		name    string
		ticks   int64
		seconds int
	}{
		{name: "the start", ticks: 0, seconds: 0},
		{name: "one second", ticks: 10_000_000, seconds: 1},
		{name: "an hour and a half", ticks: 54_000_000_000, seconds: 5400},
		{name: "a value below one second", ticks: 9_999_999, seconds: 0},
		{name: "a value below zero", ticks: -10_000_000, seconds: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := jellyfinSeconds(test.ticks); got != test.seconds {
				t.Errorf("seconds of %d = %d, want %d", test.ticks, got, test.seconds)
			}
		})
	}
}

// A listing that names an id the server does not hold answers an empty list,
// and the read is an error rather than an item with no ids.
func TestAnAbsentItemIsAnError(t *testing.T) {
	api := standJellyfinServer(t, jellyfinFixture())

	_, err := api.item(t.Context(), "item-nowhere")

	if err == nil {
		t.Fatal("reading an absent item gave no error")
	}
}
