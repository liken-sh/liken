package main

// The hooks of the fake API server's status writes, which the runner's
// tests use to play another writer and a refused write. A hook runs
// before the fake reads the write, under no lock, so it can change the
// stored object with put, and the write then meets a 409.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

type objectHooks struct {
	// beforeStatus runs before each status write, with the collection's
	// path, the object's key, and the body. It answers true to refuse
	// the write with 500.
	beforeStatus func(collection, key string, body map[string]any) bool
	// failStatus answers the next status writes with 500, the way an
	// API server that cannot reach etcd does.
	failStatus int
}

// refuseStatus runs the hooks of one status write, and answers true
// when it refused the write.
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
		answerStatus(w, http.StatusInternalServerError, "InternalError")
	}
	return refused
}
