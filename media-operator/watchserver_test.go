package main

// The pieces every watch test shares: the lines of a watch stream the
// way the API server writes them, and the dynamic client pointed at a
// test server.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
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
// answers over in-memory connections, so a watch test runs in a
// synctest bubble, and a reflector's backoff or a recheck of minutes
// takes no real time. The server closes each connection when the test
// ends, which ends a watch the test has not stopped.
func testWatcher(t *testing.T, handler http.Handler) dynamic.Interface {
	t.Helper()
	client, err := dynamic.NewForConfig(apiservertest.Start(t, handler).Config())
	mustSucceed(t, err)
	return client
}
