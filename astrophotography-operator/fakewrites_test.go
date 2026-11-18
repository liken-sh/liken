package main

// Hooks on the fake API server's status writes, for tests of a failed
// write and of a second writer. beforeStatus runs before the fake takes
// its lock, so it can change the stored object with put. The write then
// carries a stale resourceVersion and gets a 409.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

type objectHooks struct {
	// beforeStatus runs before each status write, with the collection's
	// path, the object's key, and the body. Returning true fails the
	// write with 500.
	beforeStatus func(collection, key string, body map[string]any) bool
	// failStatus is the number of upcoming status writes to fail with
	// 500, as an API server does when it cannot reach etcd.
	failStatus int
}

// refuseStatus runs the hooks for one status write. It returns true when
// it has already responded to the write with 500.
func (a *fakeAPI) refuseStatus(w http.ResponseWriter, r *http.Request, collection, key string) bool {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	var sent map[string]any
	_ = json.Unmarshal(body, &sent)
	a.mu.Lock()
	refused := a.failStatus > 0
	a.failStatus--
	hook := a.beforeStatus
	a.mu.Unlock()
	if hook != nil && hook(collection, key, sent) {
		refused = true
	}
	if refused {
		writeStatus(w, http.StatusInternalServerError, "InternalError")
	}
	return refused
}
