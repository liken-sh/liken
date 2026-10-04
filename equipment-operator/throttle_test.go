package main

// An API server that is not ready answers 429 with the seconds to wait.
// A new CRD makes it answer so for a second or two while its storage
// starts, and the shared client waits and asks again. A 429 that lasts
// longer than the client waits reaches the caller, and the starting
// lists ask again until their context ends, so the operator does not
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

// The Status body the API server sends with a 429 while a CRD's
// storage starts. It asks for the shortest wait, one second, so a test
// that meets it waits one second.
const initializingBody = `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"storage is (re)initializing","reason":"TooManyRequests","details":{"retryAfterSeconds":1},"code":429}`

// A failed delete names the object, and carries the server's own text
// without the newline the API server ends it with, so the log line
// stays whole.
func TestADeleteErrorCarriesTheServersTextWithoutTheTrailingNewline(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		delete func(*Client) error
		what   string
	}{
		{"a Receiver", func(c *Client) error { return DeleteReceiver(c, "theater") }, "deleting receiver theater: "},
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
				t.Errorf("got %q", err)
			}
		})
	}
}

// The Deployment and the node workload each start with a list, and a
// 429 there is a wait, not an exit. A 429 that asks for eleven seconds
// is more than the client waits, so the client answers it at once, and
// the starting list asks again after the wait the 429 asked for.
func TestTheStartingListsWaitOutA429(t *testing.T) {
	cases := []struct {
		name   string
		busy   int
		header string
	}{
		{"a 429 the client waits out", 1, ""},
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

				err := serve(ctx, testAPIClient(t, http.HandlerFunc(api.handle)), settings{networkDiscoveryOff: true, dial: testNetwork.dial}, testMetrics(t))

				mustSucceed(t, err)
			})
		})
		t.Run(c.name+", the node workload", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := &busyAPI{busy: c.busy, header: c.header, body: initializingBody}
				_, device := usbAdapter(cecRoom())
				node, err := newCECNode(testAPIClient(t, http.HandlerFunc(api.handle)), "node-1", device)
				mustSucceed(t, err)
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()

				mustSucceed(t, node.run(ctx))
			})
		})
	}
}

// A starting list that meets a 429 longer than the client waits asks
// again only after the wait the API server asked for, and not once a
// second with a log line each time.
func TestAStartingListWaitsWhatTheAPIServerAskedFor(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := &busyAPI{busy: 2, header: "11", body: initializingBody}
		client := testAPIClient(t, http.HandlerFunc(api.handle))
		began := time.Now()

		err := untilAnswered(t.Context(), func() error {
			_, err := ListCECBuses(client)
			return err
		})

		mustSucceed(t, err)
		mustMatch(t, time.Since(began), 2*11*time.Second)
	})
}
