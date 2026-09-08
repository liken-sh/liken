package main

// jellyfinapi.go is the client for the four calls the jellyfin role makes:
// the user list, the item listing, one item, and one person's user data. One
// server API key acts as an administrator, so one client names any user.
// Every call bounds itself with a context of ten seconds, and every failure
// is an error the caller logs. A Jellyfin that is down costs the role its
// writes and never the process.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// One request's bound, so a Jellyfin that stops answering cannot hold the bus
// reader or the write loop.
const jellyfinRequestTimeout = 10 * time.Second

// A tick is 100 nanoseconds, so ten million ticks are one second. Jellyfin
// counts every position in ticks and the store counts in seconds.
const jellyfinTicksPerSecond = 10_000_000

// The bound on one answer and on the message an answer outside 2xx carries.
// The item listing is the large one, and it is decoded as it arrives rather
// than held whole.
const (
	jellyfinAnswerLimit = 64 << 20
	jellyfinErrorLimit  = 2048
)

// The names this client gives itself in the authorization header. Jellyfin
// logs the client and the device beside the session, so a write from this
// role is legible in Jellyfin's own dashboard.
const (
	jellyfinClientName  = "liken"
	jellyfinDeviceName  = "library-operator"
	jellyfinAPIVersion  = "1"
	jellyfinItemsPath   = "/Items?recursive=true&includeItemTypes=Movie,Episode,Series&fields=ProviderIds,Path"
	jellyfinSeriesQuery = "/Items?fields=ProviderIds&ids="
	// the listing one user takes. It names the user, so every item
	// carries that person's own user data, and the caller adds the filter
	// that narrows the answer.
	jellyfinUserItemsQuery = "&recursive=true&includeItemTypes=Movie,Episode&fields=ProviderIds"
)

// The three item types the role reads. Jellyfin writes them in this case, and
// the role compares without case so a server that changes the case costs
// nothing.
const (
	jellyfinMovieType   = "Movie"
	jellyfinEpisodeType = "Episode"
	jellyfinSeriesType  = "Series"
)

// One Jellyfin server: the address, the administrator key, and the client
// that carries the requests. A test replaces the address with an httptest
// server and the client with that server's own.
type jellyfinAPI struct {
	base string
	key  string
	http *http.Client
}

// newJellyfinAPI builds the client around an address and a key. The trailing
// slash is cut, because every path below starts with one.
func newJellyfinAPI(base, key string, client *http.Client) *jellyfinAPI {
	return &jellyfinAPI{base: strings.TrimSuffix(base, "/"), key: key, http: client}
}

// The header Jellyfin reads a key from. The token is the key the Catalog
// named, and the rest names this client. Every field is quoted, which is the
// form Jellyfin's own clients send.
func (a *jellyfinAPI) authorization() string {
	return fmt.Sprintf("MediaBrowser Token=%q, Client=%q, Device=%q, DeviceId=%q, Version=%q",
		a.key, jellyfinClientName, jellyfinDeviceName, jellyfinDeviceName, jellyfinAPIVersion)
}

// One Jellyfin user: the name, which is the Person name, and the id, which
// every user-data write names.
type jellyfinUser struct {
	Name string `json:"Name"`
	ID   string `json:"Id"`
}

// One item of the listing. The provider ids are the identity the catalog
// shares with Jellyfin; an episode carries its own ids and its series' id,
// and the season and episode numbers beside them.
// the run time and the user data are the two fields the backfill reads and
// the index does not. A listing that names no user carries no user data, and
// both read as zero there.
type jellyfinItem struct {
	ID                string            `json:"Id"`
	Type              string            `json:"Type"`
	ProviderIds       map[string]string `json:"ProviderIds"`
	SeriesID          string            `json:"SeriesId"`
	ParentIndexNumber int               `json:"ParentIndexNumber"`
	IndexNumber       int               `json:"IndexNumber"`
	RunTimeTicks      int64             `json:"RunTimeTicks"`
	UserData          jellyfinUserData  `json:"UserData"`
}

// The listing's envelope. Jellyfin answers a query with the items under one
// field and the counts beside it, and the role reads the items alone.
type jellyfinItemList struct {
	Items []jellyfinItem `json:"Items"`
}

// What one user-data write states: where the person reached, whether they
// finished, and when. Jellyfin holds the position in ticks.
type jellyfinUserData struct {
	PlaybackPositionTicks int64  `json:"PlaybackPositionTicks"`
	Played                bool   `json:"Played"`
	LastPlayedDate        string `json:"LastPlayedDate"`
}

// The users of the server, with their ids. An administrator key reads them
// all.
func (a *jellyfinAPI) users(ctx context.Context) ([]jellyfinUser, error) {
	users := []jellyfinUser{}
	if err := a.get(ctx, "/Users", &users); err != nil {
		return nil, err
	}
	return users, nil
}

// Every movie, episode, and series of the server in one recursive call.
// Jellyfin has no lookup by provider id, so the whole listing is what the
// index is built from.
func (a *jellyfinAPI) items(ctx context.Context) ([]jellyfinItem, error) {
	list := jellyfinItemList{}
	if err := a.get(ctx, jellyfinItemsPath, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// the items of one user, with that person's user data on each. The query the
// caller adds is what narrows the answer, because two filters narrow together
// and the backfill needs the two answers apart.
func (a *jellyfinAPI) userItems(ctx context.Context, user, query string) ([]jellyfinItem, error) {
	list := jellyfinItemList{}
	path := "/Items?userId=" + url.QueryEscape(user) + jellyfinUserItemsQuery + query
	if err := a.get(ctx, path, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// One item by id, which the role reads for a series an episode names and the
// listing did not carry. It is the listing narrowed to one id rather than
// GET /Items/{id}, because that path answers 400 to a request that names no
// user, and a server API key names none.
func (a *jellyfinAPI) item(ctx context.Context, id string) (jellyfinItem, error) {
	list := jellyfinItemList{}
	if err := a.get(ctx, jellyfinSeriesQuery+url.QueryEscape(id), &list); err != nil {
		return jellyfinItem{}, err
	}
	if len(list.Items) == 0 {
		return jellyfinItem{}, fmt.Errorf("jellyfin has no item %s", id)
	}
	return list.Items[0], nil
}

// Writes one person's position in one item. The administrator key names the
// user, so the role needs no credential of that person.
func (a *jellyfinAPI) writeUserData(ctx context.Context, item, user string, data jellyfinUserData) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return a.post(ctx, "/UserItems/"+url.PathEscape(item)+"/UserData?userId="+url.QueryEscape(user), body)
}

// One read, decoded as it arrives. The bound covers the whole call, the
// decode included, because the body is read after the answer's header.
func (a *jellyfinAPI) get(ctx context.Context, path string, into any) error {
	bounded, done := context.WithTimeout(ctx, jellyfinRequestTimeout)
	defer done()

	response, err := a.send(bounded, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer drain(response.Body)
	if err := jellyfinStatus(path, response); err != nil {
		return err
	}
	return json.NewDecoder(io.LimitReader(response.Body, jellyfinAnswerLimit)).Decode(into)
}

// One write. Jellyfin answers a user-data write with no content, so nothing
// is decoded.
func (a *jellyfinAPI) post(ctx context.Context, path string, body []byte) error {
	bounded, done := context.WithTimeout(ctx, jellyfinRequestTimeout)
	defer done()

	response, err := a.send(bounded, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer drain(response.Body)
	return jellyfinStatus(path, response)
}

// The one place a request is built, so every call carries the same header.
func (a *jellyfinAPI) send(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, a.base+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", a.authorization())
	request.Header.Set("Accept", jsonContentType)
	if body != nil {
		request.Header.Set("Content-Type", jsonContentType)
	}
	return a.http.Do(request)
}

// An answer outside 2xx becomes an error that carries the path and the
// server's own message. The key travels in a header, so no error line holds
// it.
func jellyfinStatus(path string, response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		return nil
	}
	message, _ := io.ReadAll(io.LimitReader(response.Body, jellyfinErrorLimit))
	return fmt.Errorf("jellyfin %s: %s: %s", path, response.Status, strings.TrimSpace(string(message)))
}

// Seconds as Jellyfin counts them.
func jellyfinTicks(seconds int) int64 {
	if seconds < 0 {
		return 0
	}
	return int64(seconds) * jellyfinTicksPerSecond
}

// Ticks as the store counts them. The division rounds down, so a position
// never reads past where the person reached.
func jellyfinSeconds(ticks int64) int {
	if ticks < 0 {
		return 0
	}
	return int(ticks / jellyfinTicksPerSecond)
}
