package main

// The fake Jellyfin every test of the jellyfin role drives: the four
// endpoints the role reads and the one it writes, answered from fixture
// values, with every request recorded. A clock a test moves sits beside
// it.

import (
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

// One user-data write, as the fake server read it. stated says whether the
// body named Played at all, because a write that leaves the field out
// leaves Jellyfin's played state as it was, and data reads an absent
// field as false.
type fakeJellyfinWrite struct {
	item   string
	user   string
	data   jellyfinUserData
	stated bool
}

// A Jellyfin that answers the five endpoints from fixture values and
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
	capPage int
	// The one item whose user-data writes this server refuses. A test uses
	// it to drive a mark whose write lands on one item and fails on
	// another.
	refuseItem  string
	listings    int
	userReads   int
	seriesReads int
	writes      []fakeJellyfinWrite
	// What the server holds of each person's progress in each item, keyed
	// user/item. A write changes it the way Jellyfin does: a field the
	// write leaves out stays as it was.
	userData       map[string]jellyfinUserData
	userDataReads  int
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
	case strings.HasPrefix(request.URL.Path, "/UserItems/") && request.Method == http.MethodGet:
		f.userDataReads++
		_ = json.NewEncoder(w).Encode(f.userData[fakeUserDataKey(request)])
	case strings.HasPrefix(request.URL.Path, "/UserItems/"):
		f.write(w, request)
	default:
		http.Error(w, "no such path", http.StatusNotFound)
	}
}

// The user and the item one user-data request names.
func fakeUserDataKey(request *http.Request) string {
	item := strings.Split(strings.TrimPrefix(request.URL.Path, "/UserItems/"), "/")[0]
	return request.URL.Query().Get("userId") + "/" + item
}

// One user-data write: recorded as the body stated it, and applied to what
// the server holds, field by field.
func (f *fakeJellyfin) write(w http.ResponseWriter, request *http.Request) {
	if f.refuseItem != "" && strings.HasPrefix(request.URL.Path, "/UserItems/"+f.refuseItem+"/") {
		http.Error(w, "the server refused", http.StatusInternalServerError)
		return
	}
	body, _ := io.ReadAll(request.Body)
	data, fields := jellyfinUserData{}, map[string]json.RawMessage{}
	_ = json.Unmarshal(body, &data)
	_ = json.Unmarshal(body, &fields)
	_, stated := fields["Played"]
	f.writes = append(f.writes, fakeJellyfinWrite{
		item:   strings.Split(strings.TrimPrefix(request.URL.Path, "/UserItems/"), "/")[0],
		user:   request.URL.Query().Get("userId"),
		data:   data,
		stated: stated,
	})

	if f.userData == nil {
		f.userData = map[string]jellyfinUserData{}
	}
	key := fakeUserDataKey(request)
	held := f.userData[key]
	held.PlaybackPositionTicks = data.PlaybackPositionTicks
	if stated {
		held.Played = data.Played
	}
	if data.LastPlayedDate != "" {
		held.LastPlayedDate = data.LastPlayedDate
	}
	f.userData[key] = held
	w.WriteHeader(http.StatusNoContent)
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
		users: []jellyfinUser{{Name: "Person-A", ID: "user-a"}, {Name: "person-c", ID: "user-c"}},
		items: []jellyfinItem{
			{ID: "item-film", Type: "Movie",
				ProviderIds: map[string]string{"Tmdb": "1101", "Imdb": "tt9001101", "Tvdb": ""}},
			{ID: "item-other-film", Type: "Movie", ProviderIds: map[string]string{"Tmdb": "1102"}},
			{ID: "item-series", Type: "Series",
				ProviderIds: map[string]string{"Tmdb": "2101", "Tvdb": "3101"}},
			{ID: "item-series-3-5", Type: "Episode", SeriesID: "item-series",
				ParentIndexNumber: 3, IndexNumber: 5, ProviderIds: map[string]string{"Tmdb": "999"}},
		},
		byID: map[string]jellyfinItem{
			"item-series": {ID: "item-series", Type: "Series",
				ProviderIds: map[string]string{"Tmdb": "2101"}},
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
