package memo

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// After a successful request, only a store copy at the returned version
// is current. After a failed request, no store copy is current, because
// the object may exist at a version the operator never read.
func TestSendRecordsTheReturnedVersionOnlyOnSuccess(t *testing.T) {
	for _, c := range []struct {
		name        string
		requestErr  error
		wantCurrent bool
	}{
		{"a request that succeeds", nil, true},
		{"a request that fails", errors.New("connection refused"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			memo := New()
			memo.Note("studio", "7")

			err := memo.Send("studio", func() (string, error) { return "8", c.requestErr })

			if !errors.Is(err, c.requestErr) {
				t.Errorf("Send returned %v, want %v", err, c.requestErr)
			}
			if got := memo.Current("studio", "8"); got != c.wantCurrent {
				t.Errorf("Current(studio, 8) = %v after the request returned version 8, want %v", got, c.wantCurrent)
			}
			if memo.Current("studio", "7") {
				t.Error("Current(studio, 7) = true after the request, want false")
			}
		})
	}
}

// An object with no record is current at any version. A nil memo
// records nothing, treats every copy as current, and still sends the
// request.
func TestAnObjectWithNoRecordIsCurrent(t *testing.T) {
	var none *Versions
	sent := false
	if err := none.Send("studio", func() (string, error) { sent = true; return "8", nil }); err != nil {
		t.Fatalf("sending through a nil memo: %v", err)
	}
	none.Note("studio", "8")
	if !sent {
		t.Error("a nil memo did not call the request function, want it called")
	}
	if !none.Current("studio", "1") {
		t.Error("Current(studio, 1) = false on a nil memo, want true")
	}
	if none.Unheld(store{}) != nil {
		t.Error("Unheld on a nil memo returned keys, want none")
	}
	if !New().Current("den", "1") {
		t.Error("Current(den, 1) = false on an empty memo, want true")
	}
}

// store is a KeyGetter that holds the keys that are true in it.
type store map[string]bool

func (s store) GetByKey(key string) (any, bool, error) { return nil, s[key], nil }

// Unheld returns the keys that have a version and are missing from the
// store, such as an object the operator created a moment ago. It leaves
// out a key recorded with no version and a key the store has.
func TestUnheldReturnsVersionedKeysMissingFromTheStore(t *testing.T) {
	memo := New()
	memo.Note("den", "3")
	memo.Note("studio", "4")
	memo.Note("kitchen", "")

	got := memo.Unheld(store{"den": true})

	if !slices.Equal(got, []string{"studio"}) {
		t.Errorf("Unheld = %v, want [studio]", got)
	}
}

// After a request fails with an error other than 404, the object may
// exist, so Unheld returns it and ForgetGone keeps its record. After a
// 404 or a delete, Unheld leaves it out and ForgetGone drops its
// record.
func TestAFailedRequestLeavesTheObjectUnknown(t *testing.T) {
	for _, c := range []struct {
		name       string
		version    string
		requestErr error
		wantUnheld []string
		wantKept   bool
	}{
		{"a request that fails with no status", "8", errors.New("connection refused"), []string{"studio"}, true},
		{"a 409", "8", apiclient.ErrConflict, []string{"studio"}, true},
		{"a 404", "8", apiclient.ErrNotFound, nil, false},
		{"a delete", "", nil, nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			memo := New()
			memo.Note("studio", "7")
			_ = memo.Send("studio", func() (string, error) { return c.version, c.requestErr })

			current := memo.Current("studio", "7") || memo.Current("studio", "8")
			unheld := memo.Unheld(store{})
			memo.ForgetGone(store{}, map[string]bool{})

			if current {
				t.Error("a copy in the store is current after the request, want none current")
			}
			if !slices.Equal(unheld, c.wantUnheld) {
				t.Errorf("Unheld = %v, want %v", unheld, c.wantUnheld)
			}
			if memo.Noted("studio") != c.wantKept {
				t.Errorf("Noted(studio) = %v after ForgetGone, want %v", memo.Noted("studio"), c.wantKept)
			}
		})
	}
}

// Two requests about one object run one at a time, so the memo records
// their versions in the order the API server returned them. A request
// about another object does not wait.
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
		t.Fatal("a request about den did not finish in 5 seconds while a request about studio was in flight, want it to run at once")
	}

	second := make(chan struct{})
	go func() {
		_ = memo.Send("studio", func() (string, error) { return "9", nil })
		close(second)
	}()
	select {
	case <-second:
		t.Fatal("a second request about studio ran while the first was in flight, want it to wait")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	group.Wait()
	<-second

	if !memo.Current("studio", "9") {
		t.Error("Current(studio, 9) = false, want true after the second request returned version 9")
	}
}

// ForgetGone drops each object that the list did not return and the
// store does not have. An object the store still has keeps its record
// until the watch delivers the delete, so the store's older copy does
// not become current.
func TestAListForgetsTheObjectsThatAreGone(t *testing.T) {
	memo := New()
	memo.Note("den", "3")
	memo.Note("studio", "4")
	memo.Note("kitchen", "5")

	memo.ForgetGone(store{"studio": true}, map[string]bool{"den": true})

	if !memo.Current("den", "3") || memo.Current("den", "2") {
		t.Error("ForgetGone dropped den, which the list returned, want its record kept at version 3")
	}
	if memo.Current("studio", "2") {
		t.Error("ForgetGone dropped studio, which the store still has, want its record kept")
	}
	if !memo.Current("kitchen", "2") {
		t.Error("ForgetGone kept kitchen, which is in neither the list nor the store, want its record dropped")
	}
	var none *Versions
	none.ForgetGone(store{}, nil)
}

// Noted reports a key recorded with a version or with none, and stops
// reporting it after Forget.
func TestTheMemoForgetsOneObject(t *testing.T) {
	memo := New()
	memo.Note("den", "3")
	memo.Note("studio", "")

	if !memo.Noted("den") || !memo.Noted("studio") || memo.Noted("kitchen") {
		t.Error("Noted is false for den or studio, or true for kitchen, want true, true, false")
	}
	memo.Forget("den")
	if memo.Noted("den") || !memo.Current("den", "2") {
		t.Error("Noted(den) = true after Forget, want its record dropped")
	}
	var none *Versions
	none.Forget("den")
	if none.Noted("den") {
		t.Error("Noted(den) = true on a nil memo, want false")
	}
}

// ForgetAt drops a record only when it has the given version, and an
// empty version drops nothing.
func TestTheMemoForgetsARecordAtItsVersion(t *testing.T) {
	versions := New()
	versions.Note("a", "7")
	versions.Note("b", "")

	versions.ForgetAt("a", "6")
	versions.ForgetAt("b", "")
	if !versions.Noted("a") || !versions.Noted("b") {
		t.Fatal("ForgetAt dropped a record whose version differs from the one given, want it kept")
	}
	versions.ForgetAt("a", "7")
	if versions.Noted("a") {
		t.Error("ForgetAt kept a record at the version given, want it dropped")
	}
	var none *Versions
	none.ForgetAt("a", "7")
}

// SendList records the version of each object the list returned, so a
// store copy older than the list is not current. An object that a
// request recorded while the list was in flight keeps the request's
// version, because the list can contain a copy from before that
// request's response.
func TestSendListRecordsEachObjectThatNoRequestRecordedDuringIt(t *testing.T) {
	memo := New()
	memo.Note("den", "1")

	err := memo.SendList(func() (map[string]string, error) {
		_ = memo.Send("studio", func() (string, error) { return "9", nil })
		return map[string]string{"den": "2", "studio": "3", "kitchen": "4"}, nil
	})

	if err != nil {
		t.Fatalf("sending the list: %v", err)
	}
	if !memo.Current("den", "2") || memo.Current("den", "1") {
		t.Error("den is not current at the listed version 2, want it current")
	}
	if !memo.Current("studio", "9") {
		t.Error("the list replaced the version 9 that a request recorded while the list was in flight, want 9 kept")
	}
	if !memo.Current("kitchen", "4") || memo.Current("kitchen", "3") {
		t.Error("kitchen is not current at the listed version 4, want it current")
	}
}

// A failed list records nothing. A nil memo still sends the list.
func TestAFailedListNotesNothing(t *testing.T) {
	memo := New()
	memo.Note("den", "1")
	refused := errors.New("connection refused")

	err := memo.SendList(func() (map[string]string, error) { return nil, refused })

	if !errors.Is(err, refused) || !memo.Current("den", "1") {
		t.Errorf("SendList = %v and den current at version 1 is %v, want the list's error and true", err, memo.Current("den", "1"))
	}
	var none *Versions
	sent := false
	_ = none.SendList(func() (map[string]string, error) { sent = true; return nil, nil })
	if !sent {
		t.Error("a nil memo did not call the list function, want it called")
	}
}
