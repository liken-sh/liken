package watch

// These tests hold a store and a memo by hand, with no watch behind
// them, and count the requests a read sends to a fake API server.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// thingAPI answers a read of one thing by its key, and records each
// request.
type thingAPI struct {
	mu     sync.Mutex
	things map[string]thing
	log    []string
}

func (api *thingAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.log = append(api.log, r.Method+" "+r.URL.Path)
	item, ok := api.things[strings.TrimPrefix(r.URL.Path, "/things/")]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(item)
}

func (api *thingAPI) sent() []string {
	api.mu.Lock()
	defer api.mu.Unlock()
	sent := api.log
	api.log = nil
	return sent
}

// apiClient points the shared client at a fake API server.
func apiClient(t *testing.T, things ...thing) (*apiclient.Client, *thingAPI) {
	t.Helper()
	api := &thingAPI{things: map[string]thing{}}
	for _, item := range things {
		api.things[item.Metadata.Name] = item
	}
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return apiclient.New(server.URL, server.Client(), credentials), api
}

func read(t *testing.T, c *apiclient.Client, held informer.Held, key string) (*thing, error) {
	t.Helper()
	return ReadOne[thing](c, held, key, "/things/"+key)
}

// A ready store that holds no such object, and whose memo holds no
// record of it, answers that it does not exist, with no request. An
// object the store holds answers from the store.
func TestAReadyStoreAnswersAbsenceWithNoRequest(t *testing.T) {
	c, api := apiClient(t, newThing("a", "7", 1))
	held := informer.Held{View: readyStore(t, asObject(t, newThing("a", "7", 1))), Versions: memo.New()}

	if _, err := read(t, c, held, "b"); !errors.Is(err, apiclient.ErrNotFound) {
		t.Errorf("read(b) = %v, want ErrNotFound", err)
	}
	if got, err := read(t, c, held, "a"); err != nil || got.Metadata.ResourceVersion != "7" {
		t.Errorf("read(a) = %+v, %v; want the store's copy", got, err)
	}
	if sent := api.sent(); len(sent) != 0 {
		t.Errorf("the reads sent %q, want nothing", sent)
	}
}

// A store's copy at another version than the memo noted is read from
// the API server once. After that read, the memo holds the API
// server's version, and a store that holds it answers again. This is
// the case of a copy older than the operator's own write, and the case
// of a copy that another writer's later change reached first.
func TestACopyAtAnotherVersionIsReadOnce(t *testing.T) {
	cases := []struct {
		name   string
		stored string
		noted  string
		server string
	}{
		{"a copy older than the operator's write", "7", "8", "8"},
		{"a copy newer than the operator's write", "9", "8", "9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, api := apiClient(t, newThing("a", c.server, 1))
			versions := memo.New()
			versions.Note("a", c.noted)
			view := readyStore(t, asObject(t, newThing("a", c.stored, 1)))
			held := informer.Held{View: view, Versions: versions}

			got, err := read(t, client, held, "a")
			if err != nil || got.Metadata.ResourceVersion != c.server {
				t.Fatalf("read = %+v, %v; want the API server's copy at %s", got, err, c.server)
			}
			if err := view.Store.Update(asObject(t, newThing("a", c.server, 1))); err != nil {
				t.Fatal(err)
			}
			if got, err := read(t, client, held, "a"); err != nil || got.Metadata.ResourceVersion != c.server {
				t.Errorf("read after the store caught up = %+v, %v; want the store's copy", got, err)
			}
			if sent := api.sent(); !slices.Equal(sent, []string{"GET /things/a"}) {
				t.Errorf("the reads sent %q, want one read of a", sent)
			}
		})
	}
}

// An object the memo noted and the store does not hold, such as one
// whose write failed, is read from the API server. When the API server
// answers that it is gone, the memo forgets it, and the next read
// answers from the store with no request.
func TestANotedObjectTheStoreLacksIsReadUntilItIsGone(t *testing.T) {
	client, api := apiClient(t)
	versions := memo.New()
	versions.Note("a", "")
	held := informer.Held{View: readyStore(t), Versions: versions}

	for range 2 {
		if _, err := read(t, client, held, "a"); !errors.Is(err, apiclient.ErrNotFound) {
			t.Errorf("read = %v, want ErrNotFound", err)
		}
	}
	if sent := api.sent(); !slices.Equal(sent, []string{"GET /things/a"}) {
		t.Errorf("two reads sent %q, want one read of a", sent)
	}
}

// A store that is not ready answers nothing, and the read goes to the
// API server.
func TestAStoreThatIsNotReadyReadsTheAPIServer(t *testing.T) {
	client, api := apiClient(t, newThing("a", "7", 1))
	held := informer.Held{Versions: memo.New()}

	if got, err := read(t, client, held, "a"); err != nil || got.Metadata.ResourceVersion != "7" {
		t.Errorf("read = %+v, %v; want the API server's copy", got, err)
	}
	if _, err := read(t, client, held, "b"); !errors.Is(err, apiclient.ErrNotFound) {
		t.Errorf("read(b) = %v, want ErrNotFound", err)
	}
	if sent := api.sent(); !slices.Equal(sent, []string{"GET /things/a", "GET /things/b"}) {
		t.Errorf("the reads sent %q, want a read of each", sent)
	}
}
