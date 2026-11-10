package main

// What these tests prove. A read of the index that fails does not count as
// a read, so a Jellyfin that refused the first read at a role's start is
// read again within seconds and not a minute later. A string of failures
// waits longer after each one, up to the floor that a successful read
// holds, so a Jellyfin that keeps failing is never read in a loop.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// How many requests the server took for the index's two reads: the item
// listings and the users. A refusing fake counts no listing, so this reads
// the request log.
func indexReads(fake *fakeJellyfin) (listings, users int) {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	for _, query := range fake.queries {
		switch {
		case strings.HasPrefix(query, "/Users"):
			users++
		case strings.HasPrefix(query, "/Items?") && !strings.Contains(query, "ids="):
			listings++
		}
	}
	return listings, users
}

// The first read at the role's start fails, and Jellyfin answers a few
// seconds later. The next lookup reads again and finds the work and the
// person, well inside the one-minute floor.
func TestAFailedFirstReadDoesNotHoldOffTheNextRead(t *testing.T) {
	fake := jellyfinFixture()
	fake.status = 500
	index, clock, _ := standJellyfinIndex(t, fake)
	index.prime(t.Context())
	fake.mutex.Lock()
	fake.status = 0
	fake.mutex.Unlock()
	clock.advance(jellyfinIndexRetry)

	item, found := index.itemFor(t.Context(), map[string]string{"tmdb": "1101"}, 0, 0)
	user, known := index.userFor(t.Context(), "person-a")

	if item != "item-film" || !found {
		t.Errorf("item = %q, %v, want item-film", item, found)
	}
	if user != "user-a" || !known {
		t.Errorf("user = %q, %v, want user-a", user, known)
	}
}

// After a failed read, a lookup before the wait has run reads nothing, so
// several lookups in one pass of the write loop make one request and not
// one each. A lookup that misses after a failed read does not read twice.
func TestALookupBeforeTheRetryWaitReadsNothing(t *testing.T) {
	fake := jellyfinFixture()
	fake.status = 500
	index, clock, _ := standJellyfinIndex(t, fake)
	index.prime(t.Context())
	clock.advance(jellyfinIndexRetry - time.Second)

	index.itemFor(t.Context(), map[string]string{"tmdb": "1101"}, 0, 0)
	index.itemFor(t.Context(), map[string]string{"tmdb": "1102"}, 0, 0)
	index.userFor(t.Context(), "person-a")

	if listings, users := indexReads(fake); listings != 1 || users != 1 {
		t.Errorf("reads = %d listings and %d users, want the one of each the start made", listings, users)
	}
}

// The wait after each failure in a row doubles from the first retry wait
// and stops at the floor, so a Jellyfin that keeps failing is read at most
// once a minute, the rate a successful read allows.
func TestAStringOfFailedReadsWaitsLongerEachTime(t *testing.T) {
	cases := []struct {
		failures int
		wait     time.Duration
	}{
		{failures: 1, wait: jellyfinIndexRetry},
		{failures: 2, wait: 2 * jellyfinIndexRetry},
		{failures: 3, wait: 4 * jellyfinIndexRetry},
		{failures: 4, wait: 8 * jellyfinIndexRetry},
		{failures: 5, wait: jellyfinIndexFloor},
		{failures: 1000, wait: jellyfinIndexFloor},
	}
	for _, testCase := range cases {
		t.Run(fmt.Sprintf("%d failures", testCase.failures), func(t *testing.T) {
			if wait := jellyfinRetryWait(testCase.failures); wait != testCase.wait {
				t.Errorf("wait = %s, want %s", wait, testCase.wait)
			}
		})
	}
}

// A second failure waits longer than the first, which is the doubling seen
// from the server: the read after the first wait fails again, and a lookup
// one first wait later reads nothing.
func TestASecondFailedReadWaitsLonger(t *testing.T) {
	fake := jellyfinFixture()
	fake.status = 500
	index, clock, _ := standJellyfinIndex(t, fake)
	index.refreshItems(t.Context(), 0)
	clock.advance(jellyfinIndexRetry)
	index.refreshItems(t.Context(), 0)
	clock.advance(jellyfinIndexRetry)

	index.refreshItems(t.Context(), 0)

	if listings, _ := indexReads(fake); listings != 2 {
		t.Errorf("listings = %d, want the two reads before the second wait", listings)
	}
}
