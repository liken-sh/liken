package memo

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
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
		{"a failure", errors.New("connection refused"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			memo := New()
			memo.Note("studio", "7")

			err := memo.Send("studio", func() (string, error) { return "8", c.answer })

			if !errors.Is(err, c.answer) {
				t.Errorf("Send answered %v, want %v", err, c.answer)
			}
			if got := memo.Current("studio", "8"); got != c.wantNoted {
				t.Errorf("a copy at the answered version is current: %v, want %v", got, c.wantNoted)
			}
			if memo.Current("studio", "7") {
				t.Error("a copy at the version before the request is current")
			}
		})
	}
}

// An object the memo has not noted counts as current at any version,
// and a nil memo remembers nothing and still sends the request.
func TestAnUnnotedObjectIsCurrent(t *testing.T) {
	var none *Versions
	sent := false
	if err := none.Send("studio", func() (string, error) { sent = true; return "8", nil }); err != nil {
		t.Fatal(err)
	}
	none.Note("studio", "8")
	if !sent {
		t.Error("a nil memo did not send the request")
	}
	if !none.Current("studio", "1") {
		t.Error("a nil memo holds a copy as older")
	}
	if none.Unheld(store{}) != nil {
		t.Error("a nil memo answered keys")
	}
	if !New().Current("den", "1") {
		t.Error("an empty memo holds a copy as older")
	}
}

// store is a store that holds the named keys.
type store map[string]bool

func (s store) GetByKey(key string) (any, bool, error) { return nil, s[key], nil }

// The keys the memo noted at a version and the store does not hold are
// the objects a list from a whole store reads from the API server: one
// the operator created a moment ago. A key noted as gone, and a key the
// store holds, are not among them.
func TestTheMemoAnswersTheNotedKeysTheStoreDoesNotHold(t *testing.T) {
	memo := New()
	memo.Note("den", "3")
	memo.Note("studio", "4")
	memo.Note("kitchen", "")

	got := memo.Unheld(store{"den": true})

	if !slices.Equal(got, []string{"studio"}) {
		t.Errorf("Unheld = %v, want [studio]", got)
	}
}

// Two requests about one object run one at a time, so the memo notes
// their answers in the order the API server gave them. A request about
// another object does not wait.
func TestRequestsAboutOneObjectRunOneAtATime(t *testing.T) {
	memo := New()
	first, release := make(chan struct{}), make(chan struct{})
	var group sync.WaitGroup
	group.Go(func() {
		_ = memo.Send("studio", func() (string, error) {
			close(first)
			<-release
			return "8", nil
		})
	})
	<-first

	other := make(chan struct{})
	go func() {
		_ = memo.Send("den", func() (string, error) { return "3", nil })
		close(other)
	}()
	select {
	case <-other:
	case <-time.After(5 * time.Second):
		t.Fatal("a request about another object waited")
	}

	second := make(chan struct{})
	go func() {
		_ = memo.Send("studio", func() (string, error) { return "9", nil })
		close(second)
	}()
	select {
	case <-second:
		t.Fatal("a second request about the object ran while the first was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	group.Wait()
	<-second

	if !memo.Current("studio", "9") {
		t.Error("the memo does not hold the second answer")
	}
}

// A list forgets each noted object that it did not answer and the store
// does not hold. An object the store still holds keeps its record until
// the watch delivers the delete, so its older copy is not current.
func TestAListForgetsTheObjectsThatAreGone(t *testing.T) {
	memo := New()
	memo.Note("den", "3")
	memo.Note("studio", "4")
	memo.Note("kitchen", "5")

	memo.ForgetGone(store{"studio": true}, map[string]bool{"den": true})

	if !memo.Current("den", "3") || memo.Current("den", "2") {
		t.Error("the memo forgot den, which the list answered")
	}
	if memo.Current("studio", "2") {
		t.Error("the memo forgot studio, which the store still holds")
	}
	if !memo.Current("kitchen", "2") {
		t.Error("the memo kept kitchen, which is gone")
	}
	var none *Versions
	none.ForgetGone(store{}, nil)
}

// The memo reports a key it noted at any version, the empty one
// included, and nothing once it forgets the key.
func TestTheMemoForgetsOneObject(t *testing.T) {
	memo := New()
	memo.Note("den", "3")
	memo.Note("studio", "")

	if !memo.Noted("den") || !memo.Noted("studio") || memo.Noted("kitchen") {
		t.Error("the memo does not report the keys it noted")
	}
	memo.Forget("den")
	if memo.Noted("den") || !memo.Current("den", "2") {
		t.Error("the memo kept den after it forgot it")
	}
	var none *Versions
	none.Forget("den")
	if none.Noted("den") {
		t.Error("a nil memo noted a key")
	}
}

// ForgetAt drops a record only at the version it holds. A record of a
// newer write, or of a failed request, stays.
func TestTheMemoForgetsARecordAtItsVersion(t *testing.T) {
	versions := New()
	versions.Note("a", "7")
	versions.Note("b", "")

	versions.ForgetAt("a", "6")
	versions.ForgetAt("b", "")
	if !versions.Noted("a") || !versions.Noted("b") {
		t.Fatal("ForgetAt dropped a record at another version")
	}
	versions.ForgetAt("a", "7")
	if versions.Noted("a") {
		t.Error("ForgetAt kept a record at its own version")
	}
	var none *Versions
	none.ForgetAt("a", "7")
}
