package informer

// These tests fill a store and a memo.Versions directly, with no watch,
// and count the requests that ReadSelected sends to the fake API server
// of cache_test.go.

import (
	"errors"
	"slices"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/memo"
)

func readSelected(t *testing.T, c *apiclient.Client, watched Held, key string) (*thing, error) {
	t.Helper()
	return ReadSelected[thing](c, watched, key, thingPath(key))
}

// When a ready store lacks the object and watched.Versions has no record
// of it, ReadSelected returns ErrNotFound without a request. When the
// store has the object, ReadSelected returns the store's copy.
func TestAReadyStoreReturnsAbsenceWithNoRequest(t *testing.T) {
	api := newFakeAPI(newThing("a", "7", 1))
	c := testClient(t, api)
	watched := Held{View: storeOf(t, newThing("a", "7", 1)), Versions: memo.New()}

	if _, err := readSelected(t, c, watched, "b"); !errors.Is(err, apiclient.ErrNotFound) {
		t.Errorf("read(b) = %v, want ErrNotFound", err)
	}
	if got, err := readSelected(t, c, watched, "a"); err != nil || got.Metadata.ResourceVersion != "7" {
		t.Errorf("read(a) = %+v, %v; want the store's copy", got, err)
	}
	if sent := api.sent(); len(sent) != 0 {
		t.Errorf("the reads sent %q, want nothing", sent)
	}
}

// When the store's copy is at a different version than the recorded
// one, ReadSelected reads the API server once. After the store has
// the recorded version, ReadSelected returns the store's copy, and each
// later version too, without a request.
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
			watched := Held{View: view, Versions: versions}

			got, err := readSelected(t, client, watched, "a")
			if err != nil || got.Metadata.ResourceVersion != c.server {
				t.Fatalf("read = %+v, %v; want the API server's copy at %s", got, err, c.server)
			}
			if err := view.Store.Update(asObject(t, newThing("a", c.server, 1))); err != nil {
				t.Fatalf("updating the store to version %s: %v", c.server, err)
			}
			if got, err := readSelected(t, client, watched, "a"); err != nil || got.Metadata.ResourceVersion != c.server {
				t.Errorf("read after the store caught up = %+v, %v; want the store's copy", got, err)
			}
			if err := view.Store.Update(asObject(t, newThing("a", "20", 1))); err != nil {
				t.Fatalf("updating the store to version 20: %v", err)
			}
			if got, err := readSelected(t, client, watched, "a"); err != nil || got.Metadata.ResourceVersion != "20" {
				t.Errorf("read after another writer's change = %+v, %v; want the store's copy", got, err)
			}
			if sent := api.sent(); !slices.Equal(sent, []string{"GET /things/a"}) {
				t.Errorf("the reads sent %q, want one read of a", sent)
			}
		})
	}
}

// An object that has a record and is not in the store, such as one the
// operator created a moment ago, is read from the API server until the
// store has it.
func TestANotedObjectTheStoreLacksIsReadFromTheAPIServer(t *testing.T) {
	api := newFakeAPI(newThing("a", "8", 1))
	client := testClient(t, api)
	versions := memo.New()
	versions.Note("a", "8")
	view := storeOf(t)
	watched := Held{View: view, Versions: versions}

	if got, err := readSelected(t, client, watched, "a"); err != nil || got.Metadata.ResourceVersion != "8" {
		t.Errorf("read before the store has the created object = %+v, %v, want the API server's copy", got, err)
	}
	if err := view.Store.Add(asObject(t, newThing("a", "8", 1))); err != nil {
		t.Fatalf("adding a to the store: %v", err)
	}
	if got, err := readSelected(t, client, watched, "a"); err != nil || got.Metadata.ResourceVersion != "8" {
		t.Errorf("read after the store has the created object = %+v, %v, want the store's copy", got, err)
	}
	if sent := api.sent(); !slices.Equal(sent, []string{"GET /things/a"}) {
		t.Errorf("the reads sent %q, want one read of a", sent)
	}
}

// An object that has a record and does not exist, such as one whose
// create failed, is read from the API server once. The 404 drops the
// record, so the next read returns ErrNotFound without a request.
func TestANotedObjectThatIsGoneIsReadOnce(t *testing.T) {
	api := newFakeAPI()
	client := testClient(t, api)
	versions := memo.New()
	versions.Note("a", "")
	watched := Held{View: storeOf(t), Versions: versions}

	for range 2 {
		if _, err := readSelected(t, client, watched, "a"); !errors.Is(err, apiclient.ErrNotFound) {
			t.Errorf("read = %v, want ErrNotFound", err)
		}
	}
	if sent := api.sent(); !slices.Equal(sent, []string{"GET /things/a"}) {
		t.Errorf("two reads sent %q, want one read of a", sent)
	}
}

// ReadSelected reads the API server while the store is not ready.
func TestAStoreThatIsNotReadyReadsTheAPIServer(t *testing.T) {
	api := newFakeAPI(newThing("a", "7", 1))
	client := testClient(t, api)
	watched := Held{Versions: memo.New()}

	if got, err := readSelected(t, client, watched, "a"); err != nil || got.Metadata.ResourceVersion != "7" {
		t.Errorf("read = %+v, %v; want the API server's copy", got, err)
	}
	if _, err := readSelected(t, client, watched, "b"); !errors.Is(err, apiclient.ErrNotFound) {
		t.Errorf("read(b) = %v, want ErrNotFound", err)
	}
	if sent := api.sent(); !slices.Equal(sent, []string{"GET /things/a", "GET /things/b"}) {
		t.Errorf("the reads sent %q, want a read of each", sent)
	}
}

// ForgetStored keeps a record when the store's copy is at a different
// version or the store has no copy, and it does nothing for a store
// that is not ready.
func TestForgetStoredKeepsARecordTheStoreDoesNotHold(t *testing.T) {
	versions := memo.New()
	versions.Note("a", "8")
	versions.Note("b", "3")
	view := storeOf(t, newThing("a", "7", 1))

	ForgetStored(Held{View: view, Versions: versions}, "a", "b")
	ForgetStored(Held{Versions: versions}, "a")
	ForgetStored(Held{View: view}, "a")

	if !versions.Noted("a") || !versions.Noted("b") {
		t.Error("ForgetStored dropped the record of an object that is not in the store, want it kept")
	}
}
