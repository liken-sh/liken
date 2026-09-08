package main

// What these tests prove: the four calls the role makes reach the paths
// and carry the header Jellyfin reads a key from, one answer outside 2xx
// is an error, and the tick math is the store's seconds.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	mutex          sync.Mutex
	users          []jellyfinUser
	items          []jellyfinItem
	byID           map[string]jellyfinItem
	status         int
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
	case request.URL.Path == "/Items":
		f.listings++
		_ = json.NewEncoder(w).Encode(jellyfinItemList{Items: f.items})
	case strings.HasPrefix(request.URL.Path, "/UserItems/"):
		data := jellyfinUserData{}
		_ = json.NewDecoder(request.Body).Decode(&data)
		f.writes = append(f.writes, fakeJellyfinWrite{
			item: strings.Split(strings.TrimPrefix(request.URL.Path, "/UserItems/"), "/")[0],
			user: request.URL.Query().Get("userId"),
			data: data,
		})
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(request.URL.Path, "/Items/"):
		f.seriesReads++
		item, held := f.byID[strings.TrimPrefix(request.URL.Path, "/Items/")]
		if !held {
			http.Error(w, "no such item", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(item)
	default:
		http.Error(w, "no such path", http.StatusNotFound)
	}
}

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

// One recursive call carries every movie, episode, and series with the
// provider ids and the paths, because Jellyfin has no lookup by
// provider id.
func TestTheClientReadsTheWholeItemListing(t *testing.T) {
	fake := jellyfinFixture()
	api := standJellyfinServer(t, fake)

	items, err := api.items(t.Context())

	if err != nil {
		t.Fatalf("reading the items: %v", err)
	}
	if len(items) != 4 || items[3].SeriesID != "item-office" || items[3].IndexNumber != 5 {
		t.Errorf("items = %+v, want the four the server holds", items)
	}
	want := "/Items?recursive=true&includeItemTypes=Movie,Episode,Series&fields=ProviderIds,Path"
	if fake.queries[0] != want {
		t.Errorf("query = %q, want %q", fake.queries[0], want)
	}
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
	if want := "/Items/item-office?fields=ProviderIds"; fake.queries[0] != want {
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

	_, err := api.items(context.Background())

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
