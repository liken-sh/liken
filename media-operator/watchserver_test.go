package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// watchLine returns one event of a watch stream.
func watchLine(eventType string, object json.RawMessage) string {
	line, _ := json.Marshal(map[string]any{"type": eventType, "object": object})
	return string(line)
}

// initialEventsEnd returns the BOOKMARK event that the API server sends
// after the last initial event of a streaming list (a watch with
// sendInitialEvents).
func initialEventsEnd(apiVersion, kind, version string) string {
	return fmt.Sprintf(`{"type":"BOOKMARK","object":{"apiVersion":%q,"kind":%q,"metadata":{"resourceVersion":%q,"annotations":{"k8s.io/initial-events-end":"true"}}}}`,
		apiVersion, kind, version)
}

// testWatcher returns a dynamic client of a test server that runs
// handler. The server uses in-memory connections, so a watch test can
// run in a synctest bubble with a reflector's backoff on the fake clock.
// The server closes its connections when the test ends, which ends any
// watch that the test did not stop.
func testWatcher(t *testing.T, handler http.Handler) dynamic.Interface {
	t.Helper()
	client, err := dynamic.NewForConfig(apiservertest.Start(t, handler).Config())
	mustSucceed(t, err)
	return client
}
