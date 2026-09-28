package main

// The pieces every watch test shares: the lines of a watch stream the
// way the API server writes them, the dynamic client pointed at a test
// server, and the wait for an outcome another goroutine reaches.

import (
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

// testWatcher points a dynamic client at a test server. The server
// cuts every connection before it closes, because a watch the test has
// not stopped yet holds its stream open, and Close waits for every
// request to finish.
func testWatcher(t *testing.T, handler http.Handler) dynamic.Interface {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
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
