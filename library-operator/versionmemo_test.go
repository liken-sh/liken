package main

// These tests cover the memo of versionmemo.go, which every liken-sh
// operator holds in the same form.

import (
	"errors"
	"testing"
)

// A request the API server answers notes the version it answered, so a
// store's copy at that version is current and a copy at any other
// version is not. A failed request notes that the operator holds no
// copy of the API server's: a 404, a 409, or a write whose answer was
// lost and may have landed.
func TestTheMemoNotesWhatTheAPIServerAnswered(t *testing.T) {
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
			memo.note("house/movies", "7")

			err := memo.send("house/movies", func() (string, error) { return "8", c.answer })

			if !errors.Is(err, c.answer) {
				t.Errorf("send answered %v, want %v", err, c.answer)
			}
			if got := memo.current("house/movies", "8"); got != c.wantNoted {
				t.Errorf("a copy at the answered version is current: %v, want %v", got, c.wantNoted)
			}
			if memo.current("house/movies", "7") {
				t.Error("a copy at the version before the request is current")
			}
		})
	}
}

// An object the memo has not noted counts as current at any version,
// and a nil memo remembers nothing and still sends the request.
func TestAnUnnotedObjectIsCurrent(t *testing.T) {
	var none *versionMemo
	sent := false
	if err := none.send("house/movies", func() (string, error) { sent = true; return "8", nil }); err != nil {
		t.Fatal(err)
	}
	if !sent {
		t.Error("a nil memo did not send the request")
	}
	if !none.current("house/movies", "1") {
		t.Error("a nil memo holds a copy as older")
	}
	if !newVersionMemo().current("house/series", "1") {
		t.Error("an empty memo holds a copy as older")
	}
}
