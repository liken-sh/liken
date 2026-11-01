package informer

// These tests hold a store and a memo by hand, with no watch behind
// them, and count the requests that ReadSelected sends to the fake API
// server of cache_test.go.

import (
	"errors"
	"slices"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/memo"
)

func readSelected(t *testing.T, c *apiclient.Client, held Held, key string) (*thing, error) {
	t.Helper()
	return ReadSelected[thing](c, held, key, thingPath(key))
}

// A ready store that holds no such object, and whose memo holds no
// record of it, answers that it does not exist, with no request. An
// object the store holds answers from the store.
func TestAReadyStoreAnswersAbsenceWithNoRequest(t *testing.T) {
	api := newFakeAPI(newThing("a", "7", 1))
	c := testClient(t, api)
	held := Held{View: storeOf(t, newThing("a", "7", 1)), Versions: memo.New()}

	if _, err := readSelected(t, c, held, "b"); !errors.Is(err, apiclient.ErrNotFound) {
		t.Errorf("read(b) = %v, want ErrNotFound", err)
	}
	if got, err := readSelected(t, c, held, "a"); err != nil || got.Metadata.ResourceVersion != "7" {
		t.Errorf("read(a) = %+v, %v; want the store's copy", got, err)
	}
	if sent := api.sent(); len(sent) != 0 {
		t.Errorf("the reads sent %q, want nothing", sent)
	}
}

// A store's copy at another version than the memo noted is read from
// the API server once. After that read, the memo holds the API
// server's version, and a store that holds it answers again, and from
// then on answers each later version with no request. This is the case
// of a copy older than the operator's own write, and the case of a copy
// that another writer's later change reached first.
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
			api := newFakeAPI(newThing("a", c.server, 1))
			client := testClient(t, api)
			versions := memo.New()
			versions.Note("a", c.noted)
			view := storeOf(t, newThing("a", c.stored, 1))
			held := Held{View: view, Versions: versions}

			got, err := readSelected(t, client, held, "a")
			if err != nil || got.Metadata.ResourceVersion != c.server {
				t.Fatalf("read = %+v, %v; want the API server's copy at %s", got, err, c.server)
			}
			if err := view.Store.Update(asObject(t, newThing("a", c.server, 1))); err != nil {
				t.Fatal(err)
			}
			if got, err := readSelected(t, client, held, "a"); err != nil || got.Metadata.ResourceVersion != c.server {
				t.Errorf("read after the store caught up = %+v, %v; want the store's copy", got, err)
			}
			// Another writer's later change reaches the store. The memo
			// dropped its record once the store held the noted version,
			// so the store answers with no request.
			if err := view.Store.Update(asObject(t, newThing("a", "20", 1))); err != nil {
				t.Fatal(err)
			}
			if got, err := readSelected(t, client, held, "a"); err != nil || got.Metadata.ResourceVersion != "20" {
				t.Errorf("read after another writer's change = %+v, %v; want the store's copy", got, err)
			}
			if sent := api.sent(); !slices.Equal(sent, []string{"GET /things/a"}) {
				t.Errorf("the reads sent %q, want one read of a", sent)
			}
		})
	}
}

// An object the memo noted and the store does not hold, such as one
// the operator created a moment ago, is read from the API server until
// the store holds it.
func TestANotedObjectTheStoreLacksIsReadFromTheAPIServer(t *testing.T) {
	api := newFakeAPI(newThing("a", "8", 1))
	client := testClient(t, api)
	versions := memo.New()
	versions.Note("a", "8")
	view := storeOf(t)
	held := Held{View: view, Versions: versions}

	if got, err := readSelected(t, client, held, "a"); err != nil || got.Metadata.ResourceVersion != "8" {
		t.Errorf("read before the store holds the create = %+v, %v; want the API server's copy", got, err)
	}
	if err := view.Store.Add(asObject(t, newThing("a", "8", 1))); err != nil {
		t.Fatal(err)
	}
	if got, err := readSelected(t, client, held, "a"); err != nil || got.Metadata.ResourceVersion != "8" {
		t.Errorf("read after the store holds the create = %+v, %v; want the store's copy", got, err)
	}
	if sent := api.sent(); !slices.Equal(sent, []string{"GET /things/a"}) {
		t.Errorf("the reads sent %q, want one read of a", sent)
	}
}

// An object the memo noted that is gone, such as one whose write
// failed, is read from the API server once. When the API server answers
// that it is gone, the memo forgets it, and the next read answers from
// the store with no request.
func TestANotedObjectThatIsGoneIsReadOnce(t *testing.T) {
	api := newFakeAPI()
	client := testClient(t, api)
	versions := memo.New()
	versions.Note("a", "")
	held := Held{View: storeOf(t), Versions: versions}

	for range 2 {
		if _, err := readSelected(t, client, held, "a"); !errors.Is(err, apiclient.ErrNotFound) {
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
	api := newFakeAPI(newThing("a", "7", 1))
	client := testClient(t, api)
	held := Held{Versions: memo.New()}

	if got, err := readSelected(t, client, held, "a"); err != nil || got.Metadata.ResourceVersion != "7" {
		t.Errorf("read = %+v, %v; want the API server's copy", got, err)
	}
	if _, err := readSelected(t, client, held, "b"); !errors.Is(err, apiclient.ErrNotFound) {
		t.Errorf("read(b) = %v, want ErrNotFound", err)
	}
	if sent := api.sent(); !slices.Equal(sent, []string{"GET /things/a", "GET /things/b"}) {
		t.Errorf("the reads sent %q, want a read of each", sent)
	}
}

// ForgetStored keeps a record while the store's copy is at another
// version or the store holds no copy, and does nothing for a store that
// is not ready.
func TestForgetStoredKeepsARecordTheStoreDoesNotHold(t *testing.T) {
	versions := memo.New()
	versions.Note("a", "8")
	versions.Note("b", "3")
	view := storeOf(t, newThing("a", "7", 1))

	ForgetStored(Held{View: view, Versions: versions}, "a", "b")
	ForgetStored(Held{Versions: versions}, "a")
	ForgetStored(Held{View: view}, "a")

	if !versions.Noted("a") || !versions.Noted("b") {
		t.Error("ForgetStored dropped a record the store does not hold")
	}
}
