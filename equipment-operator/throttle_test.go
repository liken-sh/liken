package main

// The API server returns 429 while it starts the storage of a new CRD.
// The shared client waits and sends the request again, up to
// maxThrottleWait, and returns a longer 429 to the caller. The lists at
// startup then retry until their context ends, so the operator does not
// exit.

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// busyAPI returns 429 to the first busy requests and an empty list after
// that.
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

// initializingBody is the Status that the API server sends with a 429
// while a CRD's storage starts.
const initializingBody = `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"storage is (re)initializing","reason":"TooManyRequests","details":{"retryAfterSeconds":1},"code":429}`

// The error drops the newline that ends the server's text, so it stays
// on one log line.
func TestADeleteErrorIncludesTheServersTextWithoutTheTrailingNewline(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		delete func(*Client) error
		what   string
	}{
		{"a Receiver", func(c *Client) error { return DeleteReceiver(c, "theater") }, "deleting Receiver theater: "},
		{"a CECBus", func(c *Client) error { return DeleteCECBus(c, "den") }, "deleting CECBus den: "},
		{"a Television", func(c *Client) error { return DeleteTelevision(c, "den") }, "deleting Television den: "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte("forbidden\n"))
			}))

			err := c.delete(client)

			if err == nil || !strings.HasPrefix(err.Error(), c.what) || !strings.HasSuffix(err.Error(), ": forbidden") {
				t.Errorf("the error is %q, want the prefix %q and the suffix \": forbidden\"", err, c.what)
			}
		})
	}
}

// A Retry-After of 11 is longer than the client waits, so the client
// returns the 429 at once and the starting list retries it.
func TestTheStartingListsWaitOutA429(t *testing.T) {
	cases := []struct {
		name   string
		busy   int
		header string
	}{
		{"a 429 that the client waits out", 1, ""},
		{"a 429 longer than the client waits", 3, "11"},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name+", the Deployment", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := &busyAPI{busy: c.busy, header: c.header, body: initializingBody}
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()

				err := serve(ctx, testAPIClient(t, http.HandlerFunc(api.handle)), settings{namespace: testNamespace, networkDiscoveryOff: true, dial: testNetwork.dial, listen: testNetwork.bind}, testMetrics(t))

				mustSucceed(t, err)
			})
		})
		t.Run(c.name+", the node workload", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := &busyAPI{busy: c.busy, header: c.header, body: initializingBody}
				_, device := usbAdapter(fakeCECBus())
				node, err := newCECNode(testAPIClient(t, http.HandlerFunc(api.handle)), testNamespace, "node-1", device)
				mustSucceed(t, err)
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()

				mustSucceed(t, node.run(ctx))
			})
		})
	}
}

// A starting list waits for the full Retry-After before it retries, and
// does not retry and log every second.
func TestAStartingListWaitsForTheRetryAfterTheAPIServerSent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := &busyAPI{busy: 2, header: "11", body: initializingBody}
		client := testAPIClient(t, http.HandlerFunc(api.handle))
		began := time.Now()

		err := retryWhileThrottled(t.Context(), func() error {
			_, err := ListCECBuses(client)
			return err
		})

		mustSucceed(t, err)
		mustMatch(t, time.Since(began), 2*11*time.Second)
	})
}
