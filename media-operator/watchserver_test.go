package main

// The pieces every watch test shares: the lines of a watch stream the
// way the API server writes them, the dynamic client pointed at a test
// server, and the wait for an outcome another goroutine reaches.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// The bounds on every wait in the watch tests: long enough that a
// loaded machine still passes, short enough that a broken watch fails
// in seconds.
const (
	watchTimeout    = 5 * time.Second
	watchQuietSpell = 100 * time.Millisecond
)

// watchLine is one event on a watch stream.
func watchLine(eventType string, object json.RawMessage) string {
	line, _ := json.Marshal(map[string]any{"type": eventType, "object": object})
	return string(line)
}

// initialEventsEnd is the bookmark that ends the initial events of a
// streaming list.
func initialEventsEnd(apiVersion, kind, version string) string {
	return fmt.Sprintf(`{"type":"BOOKMARK","object":{"apiVersion":%q,"kind":%q,"metadata":{"resourceVersion":%q,"annotations":{"k8s.io/initial-events-end":"true"}}}}`,
		apiVersion, kind, version)
}

// testWatcher points a dynamic client at a test server. A watch the
// test has not stopped yet holds its stream open, and Close waits for
// every request to finish. A test's cleanups can close the server
// before they stop the watch, and the reflector then opens a new
// watch after the server cut the old connections. So the cleanup ends
// the context of every request, a request that arrives later included,
// before it cuts the connections and closes the server.
func testWatcher(t *testing.T, handler http.Handler) dynamic.Interface {
	t.Helper()
	closing, closed := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithCancel(r.Context())
		defer context.AfterFunc(closing, cancel)()
		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(func() {
		closed()
		server.CloseClientConnections()
		server.Close()
	})
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	mustSucceed(t, err)
	return client
}

// until waits for an outcome with a deadline, because a watch is
// another goroutine and a test asserts what the owner holds, not when
// the goroutine ran.
func until(t *testing.T, complaint string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(watchTimeout)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal(complaint)
		}
		time.Sleep(watchQuietSpell / 10)
	}
}

// closedWithin reports whether a channel closes before the wait runs
// out.
func closedWithin(channel <-chan struct{}, wait time.Duration) bool {
	select {
	case <-channel:
		return true
	case <-time.After(wait):
		return false
	}
}
