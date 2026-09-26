package main

// An API server that is not ready answers 429 with the seconds to wait.
// A new CRD makes it answer so for a second or two while its storage
// starts, and the operator waits and asks again instead of exiting.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// busyAPI answers 429 to the first busy requests, the way the API
// server answers while a new CRD's storage starts, and the collection
// after that.
type busyAPI struct {
	mutex    sync.Mutex
	busy     int
	requests int
	header   string
	body     string
}

func (a *busyAPI) handle(w http.ResponseWriter, r *http.Request) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.requests++
	if a.requests <= a.busy {
		if a.header != "" {
			w.Header().Set("Retry-After", a.header)
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(a.body))
		return
	}
	_, _ = w.Write([]byte(`{"metadata":{"resourceVersion":"5"},"items":[]}`))
}

func (a *busyAPI) count() int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.requests
}

// The Status body the API server sends with a 429 while a CRD's
// storage starts.
const initializingBody = `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"storage is (re)initializing","reason":"TooManyRequests","details":{"retryAfterSeconds":2},"code":429}`

func TestA429StatesHowLongToWait(t *testing.T) {
	cases := []struct {
		name   string
		header string
		body   string
		wait   time.Duration
	}{
		{"the Status body", "", initializingBody, 2 * retryAfterUnit},
		{"the Retry-After header", "3", initializingBody, 3 * retryAfterUnit},
		{"neither", "", "slow down", retryAfterUnit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &busyAPI{busy: 1, header: c.header, body: c.body}

			_, err := ListCECBuses(testAPIClient(t, http.HandlerFunc(api.handle)))

			var throttled *throttledError
			if !errors.As(err, &throttled) || throttled.wait != c.wait {
				t.Fatalf("got %v, want a wait of %s", err, c.wait)
			}
			if !strings.Contains(err.Error(), "429 Too Many Requests: "+c.body) {
				t.Errorf("the error leaves out the server's text: %v", err)
			}
		})
	}
}

func TestRetryThrottledWaitsAndAsksAgain(t *testing.T) {
	shorten(t, &retryAfterUnit, time.Millisecond)
	api := &busyAPI{busy: 2, body: initializingBody}
	client := testAPIClient(t, http.HandlerFunc(api.handle))

	err := retryThrottled(t.Context(), func() error {
		_, err := ListCECBuses(client)
		return err
	})

	mustSucceed(t, err)
	mustMatch(t, api.count(), 3)
}

func TestRetryThrottledStopsWithItsContext(t *testing.T) {
	api := &busyAPI{busy: 1000, body: initializingBody}
	client := testAPIClient(t, http.HandlerFunc(api.handle))
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	err := retryThrottled(ctx, func() error {
		_, err := ListCECBuses(client)
		return err
	})

	var throttled *throttledError
	if !errors.As(err, &throttled) {
		t.Errorf("got %v, want the last 429", err)
	}
}

// Any other failure is the caller's to handle, at once.
func TestRetryThrottledPassesOtherErrorsThrough(t *testing.T) {
	calls := 0
	failure := errors.New("refused")

	err := retryThrottled(t.Context(), func() error {
		calls++
		return failure
	})

	if !errors.Is(err, failure) || calls != 1 {
		t.Errorf("got %v after %d calls", err, calls)
	}
}

// The Deployment and the node workload each start with a list, and a
// 429 there is a wait, not an exit.
func TestTheStartingListsWaitOutA429(t *testing.T) {
	shorten(t, &retryAfterUnit, time.Millisecond)
	t.Run("the Deployment", func(t *testing.T) {
		api := &busyAPI{busy: 2, body: initializingBody}
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		err := serve(ctx, testAPIClient(t, http.HandlerFunc(api.handle)), "127.0.0.1:1", testMetrics(t))

		mustSucceed(t, err)
	})
	t.Run("the node workload", func(t *testing.T) {
		api := &busyAPI{busy: 2, body: initializingBody}
		_, device := usbAdapter(cecRoom())
		node, err := newCECNode(testAPIClient(t, http.HandlerFunc(api.handle)), "node-1", device)
		mustSucceed(t, err)
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		mustSucceed(t, node.run(ctx))
	})
}
