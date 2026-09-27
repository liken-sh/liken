package main

// These tests cover what wakes the loop for a PairingRequest: a change
// to a request that needs a pass, and the clock at the moment a
// finished request's TTL is up. A finished request inside its TTL
// wakes nothing.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
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

// requestJSON is one request as the API server sends it in a list or
// an event.
func requestJSON(t *testing.T, request PairingRequest) string {
	t.Helper()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// watchRequests runs the watcher against a server until the test ends.
func watchRequests(t *testing.T, server *watchServer) <-chan struct{} {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	wakes := watchPairingRequests(ctx, testClient(t, server), time.Now)
	t.Cleanup(func() {
		cancel()
		for range wakes {
		}
	})
	return wakes
}

func awaitWake(t *testing.T, wakes <-chan struct{}, within time.Duration) {
	t.Helper()
	select {
	case <-wakes:
	case <-time.After(within):
		t.Fatalf("the loop had no wake within %s", within)
	}
}

func TestANewRequestWakesTheLoopAtOnce(t *testing.T) {
	created := fmt.Sprintf(`{"type":"ADDED","object":%s}`, requestJSON(t, *openRequest("")))
	server := newWatchServer(pairingRequestsPath(), "[]", []string{created, holdOpen})

	awaitWake(t, watchRequests(t, server), 5*time.Second)
}

func TestAFinishedRequestWakesTheLoopWhenItsTTLIsUp(t *testing.T) {
	// The TTL is up between one and two seconds from now. timestamp
	// keeps whole seconds, so the exact moment depends on when the
	// test starts.
	seconds := 60
	request := *openRequest(testDevice)
	request.Spec.TTLSecondsAfterFinished = &seconds
	request.Status = PairingRequestStatus{
		Phase:      phasePaired,
		FinishedAt: timestamp(time.Now().Add(-58 * time.Second)),
	}
	server := newWatchServer(pairingRequestsPath(), "["+requestJSON(t, request)+"]", []string{holdOpen})
	wakes := watchRequests(t, server)
	server.awaitWatches(t, 1)

	select {
	case <-wakes:
		t.Fatal("a request inside its TTL woke the loop")
	case <-time.After(500 * time.Millisecond):
	}
	awaitWake(t, wakes, 3*time.Second)
}
