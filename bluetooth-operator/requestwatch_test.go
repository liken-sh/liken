package main

// These tests cover what wakes the loop for a PairingRequest: a change
// to a request that needs a pass, and the clock at the moment a
// finished request's TTL is up. A finished request inside its TTL
// wakes nothing.

import (
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/tools/cache"
)

// The watcher wakes the loop for a request that is unfinished and for
// one whose collection is due, and for nothing else. On an idle
// cluster a change to a finished request must not trigger a pass.
func TestRequestsNeedAPass(t *testing.T) {
	cases := []struct {
		name    string
		request PairingRequest
		want    bool
	}{
		{
			name:    "a request nobody has run yet",
			request: PairingRequest{},
			want:    true,
		},
		{
			name:    "an open window",
			request: PairingRequest{Status: PairingRequestStatus{Phase: phaseOpen}},
			want:    true,
		},
		{
			name: "a finished request inside its TTL",
			request: PairingRequest{Status: PairingRequestStatus{
				Phase:      phasePaired,
				FinishedAt: timestamp(testNow.Add(-time.Hour)),
			}},
			want: false,
		},
		{
			name: "a finished request past its TTL",
			request: PairingRequest{Status: PairingRequestStatus{
				Phase:      phaseExpired,
				FinishedAt: timestamp(testNow.Add(-25 * time.Hour)),
			}},
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := requestsNeedAPass([]PairingRequest{c.request}, testNow); got != c.want {
				t.Fatalf("requestsNeedAPass = %t, want %t", got, c.want)
			}
		})
	}
}

func TestTheNextCollectionIsTheEarliestOneStillAhead(t *testing.T) {
	ttl := func(seconds int) *int { return &seconds }
	finished := func(ago time.Duration, seconds int) PairingRequest {
		return PairingRequest{
			Spec:   PairingRequestSpec{TTLSecondsAfterFinished: ttl(seconds)},
			Status: PairingRequestStatus{Phase: phasePaired, FinishedAt: timestamp(testNow.Add(-ago))},
		}
	}
	cases := []struct {
		name     string
		requests []PairingRequest
		want     time.Time
	}{
		{
			name: "no request",
		},
		{
			name:     "an open window has no collection",
			requests: []PairingRequest{{Status: PairingRequestStatus{Phase: phaseOpen}}},
		},
		{
			name:     "a collection already due is the pass's work, not the clock's",
			requests: []PairingRequest{finished(2*time.Hour, 3600)},
		},
		{
			name:     "one finished request",
			requests: []PairingRequest{finished(time.Hour, 7200)},
			want:     testNow.Add(time.Hour),
		},
		{
			name:     "the earliest of two",
			requests: []PairingRequest{finished(time.Hour, 7200), finished(time.Hour, 3700)},
			want:     testNow.Add(100 * time.Second),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := nextCollection(c.requests, testNow)
			if !got.Equal(c.want) {
				t.Fatalf("nextCollection = %s, want %s", got, c.want)
			}
		})
	}
}

// watchRequests runs the request watcher against a server until the
// test ends.
func watchRequests(t *testing.T, server *watchServer) <-chan struct{} {
	t.Helper()
	wakes, _ := watchPairingRequests(t.Context(), testWatcher(t, server.handler(t)), time.Now)
	t.Cleanup(func() {
		for range wakes {
		}
	})
	return wakes
}

func TestANewRequestWakesTheLoopAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		created := fmt.Sprintf(`{"type":"ADDED","object":%s}`, encode(t, openRequest("")))
		server := newWatchServer(pairingRequestsPath(), "PairingRequestList", []string{"[]"}, []string{created, holdOpen})
		wakes := watchRequests(t, server)
		synctest.Wait()

		if !woke(wakes) {
			t.Fatal("the new request did not wake the loop")
		}
	})
}

// The clock fires at the moment the TTL is up, and not before.
func TestAFinishedRequestWakesTheLoopWhenItsTTLIsUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// The bubble's clock starts on a whole second, so the TTL is up
		// exactly two seconds from now.
		seconds := 60
		request := *openRequest(testDevice)
		request.Spec.TTLSecondsAfterFinished = &seconds
		request.Status = PairingRequestStatus{
			Phase:      phasePaired,
			FinishedAt: timestamp(time.Now().Add(-58 * time.Second)),
		}
		server := newWatchServer(pairingRequestsPath(), "PairingRequestList", []string{"[" + encode(t, request) + "]"}, []string{holdOpen})
		wakes := watchRequests(t, server)

		time.Sleep(2*time.Second - time.Nanosecond)
		synctest.Wait()
		if server.opened() != 1 || woke(wakes) {
			t.Fatal("a request inside its TTL woke the loop")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if !woke(wakes) {
			t.Fatal("the loop had no wake when the TTL was up")
		}
	})
}

// A deleted request can arrive as a tombstone that holds no copy of
// it. The watcher forgets the request by the tombstone's key, so a
// request that no longer exists does not wake the loop again.
func TestARequestDeletedWithNoCopyIsForgotten(t *testing.T) {
	wakes := make(chan struct{}, 1)
	request := *openRequest("")
	watcher := &requestWatcher{now: time.Now, wake: wakes, held: map[string]PairingRequest{requestKey(request): request}}

	watcher.apply(cache.DeletedFinalStateUnknown{Key: requestKey(request)}, true)
	watcher.fire()

	if len(wakes) != 0 {
		t.Fatal("a deleted request woke the loop")
	}
}
