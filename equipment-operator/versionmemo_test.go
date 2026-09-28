package main

// These tests cover the memo of objectcache.go, which every liken-sh
// operator holds in the same form.

import (
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
)

// A request the API server answers notes the version it answered, so a
// store's copy at that version is current and a copy at any other
// version is not. A failed request notes that the operator holds no
// copy of the API server's: a 404, a 409, or a write whose answer was
// lost and may have landed.
func TestTheMemoNotesWhatTheAPIServerAnswered(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		answer    error
		wantNoted bool
	}{
		{"an answer", nil, true},
		{"a 404", ErrNotFound, false},
		{"a 409", ErrConflict, false},
		{"another failure", errors.New("connection refused"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			memo := newVersionMemo()
			memo.note("studio", "7")

			err := memo.send("studio", func() (string, error) { return "8", c.answer })

			if !errors.Is(err, c.answer) {
				t.Errorf("send answered %v, want %v", err, c.answer)
			}
			if got := memo.current("studio", "8"); got != c.wantNoted {
				t.Errorf("a copy at the answered version is current: %v, want %v", got, c.wantNoted)
			}
			if memo.current("studio", "7") {
				t.Error("a copy at the version before the request is current")
			}
		})
	}
}

// An object the memo has not noted counts as current at any version,
// and a nil memo remembers nothing and still sends the request.
func TestAnUnnotedObjectIsCurrent(t *testing.T) {
	t.Parallel()
	var none *versionMemo
	sent := false
	if err := none.send("studio", func() (string, error) { sent = true; return "8", nil }); err != nil {
		t.Fatal(err)
	}
	if !sent {
		t.Error("a nil memo did not send the request")
	}
	if !none.current("studio", "1") {
		t.Error("a nil memo holds a copy as older")
	}
	if !newVersionMemo().current("den", "1") {
		t.Error("an empty memo holds a copy as older")
	}
}

// The keys the memo noted at a version and the store does not hold are
// the objects a list from a whole store reads from the API server: one
// this operator created a moment ago. A key noted as gone, and a key
// the store holds, are not among them.
func TestTheMemoAnswersTheNotedKeysTheStoreDoesNotHold(t *testing.T) {
	t.Parallel()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	held := &unstructured.Unstructured{}
	held.SetName("den")
	if err := store.Add(held); err != nil {
		t.Fatal(err)
	}
	memo := newVersionMemo()
	memo.note("den", "3")
	memo.note("studio", "4")
	memo.note("kitchen", "")

	got := memo.unheld(store)

	if len(got) != 1 || got[0] != "studio" {
		t.Errorf("unheld = %v, want [studio]", got)
	}
}
