package informer

// These tests hold a copy to the rule in writes.go: after this
// process's own write, the copy does not answer for the object until
// the watch delivers that write.

import (
	"context"
	"testing"
	"time"
)

// awaitAnswer waits until the copy answers for a, with the version it
// holds.
func awaitAnswer(t *testing.T, c *Collection, version string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got, _, ok := Get[thing](c, "a"); ok && got.Metadata.ResourceVersion == version {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the copy never answered with version %s", version)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Through the reflector: a write the copy does not hold yet makes the
// copy refuse a read of that object and a list, until the watch
// delivers the write's version.
func TestACopyDoesNotAnswerUntilItHoldsThisProcessesWrite(t *testing.T) {
	server := newWatchServer([][]thing{{newThing("a", "7", 1)}},
		[]string{pause, event("MODIFIED", newThing("a", "150", 1)), holdOpen})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := Start(ctx, testClient(t, server), testSource, Options{})
	awaitSynced(t, c)

	c.Wrote("a", "150")

	_, _, got := Get[thing](c, "a")
	_, listed := List[thing](c)
	if got || listed {
		t.Fatal("the copy answered before it held the write")
	}
	server.release()
	awaitAnswer(t, c, "150")
	if _, listed := List[thing](c); !listed {
		t.Error("the copy still refused a list after it held the write")
	}
}

// A write whose version the copy already holds, because the watch
// delivered it before the write's answer arrived, leaves the copy
// answering.
func TestAWriteTheCopyAlreadyHoldsKeepsItAnswering(t *testing.T) {
	server := newWatchServer([][]thing{{newThing("a", "7", 1)}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := Start(ctx, testClient(t, server), testSource, Options{})
	awaitSynced(t, c)

	c.Wrote("a", "7")

	if _, _, ok := Get[thing](c, "a"); !ok {
		t.Error("the copy refused a read of a write it already held")
	}
}

// A write whose outcome is unknown makes the copy refuse, until a
// direct read shows the version the copy holds, or until the watch
// delivers the object's next change.
func TestAWriteWithAnUnknownOutcomeSettles(t *testing.T) {
	cases := []struct {
		name   string
		settle func(c *Collection, server *watchServer)
	}{
		{"by a direct read of the version the copy holds", func(c *Collection, _ *watchServer) { c.Observed("a", "7") }},
		{"by the next change", func(_ *Collection, server *watchServer) { server.release() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newWatchServer([][]thing{{newThing("a", "7", 1)}},
				[]string{pause, event("MODIFIED", newThing("a", "150", 1)), holdOpen})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			c := Start(ctx, testClient(t, server), testSource, Options{})
			awaitSynced(t, c)
			c.Wrote("a", "")
			if _, _, ok := Get[thing](c, "a"); ok {
				t.Fatal("the copy answered after a write with an unknown outcome")
			}

			tc.settle(c, server)

			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, _, ok := Get[thing](c, "a"); ok {
					return
				}
				if time.Now().After(deadline) {
					t.Fatal("the copy never answered again")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

// A nil collection takes a record and still never answers.
func TestANilCollectionTakesARecord(t *testing.T) {
	var missing *Collection
	missing.Wrote("a", "150")
	missing.Observed("a", "150")
	if _, _, ok := Get[thing](missing, "a"); ok {
		t.Error("a nil collection answered a read")
	}
}

// A later version of the object stands in for the write: the watch can
// deliver the write and a later change before the write's answer
// arrives, and a read after a gap reports only the newest version.
func TestALaterVersionSettlesAWrite(t *testing.T) {
	cases := []struct {
		name  string
		wrote string
		held  string
		want  bool
	}{
		{"the same version", "150", "150", true},
		{"a later version", "150", "160", true},
		{"an earlier version", "150", "140", false},
		{"a version that is not a number, the same", "a", "a", true},
		{"a version that is not a number, another", "a", "b", false},
		{"no version held", "150", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := atLeast(c.held, c.wrote); got != c.want {
				t.Errorf("atLeast(%q, %q) = %v, want %v", c.held, c.wrote, got, c.want)
			}
		})
	}
}

// Through the reflector: a write whose answer arrives after the watch
// already delivered a later change leaves the copy answering.
func TestAWriteOvertakenByALaterChangeKeepsTheCopyAnswering(t *testing.T) {
	server := newWatchServer([][]thing{{newThing("a", "160", 1)}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := Start(ctx, testClient(t, server), testSource, Options{})
	awaitSynced(t, c)

	c.Wrote("a", "150")

	if _, _, ok := Get[thing](c, "a"); !ok {
		t.Error("the copy refused a read after a later change than the write")
	}
}
