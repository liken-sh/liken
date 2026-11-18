package main

// Helpers that read what the fake API server (fakeobjects_test.go)
// stores, and switches that make it fail requests.

import (
	"encoding/json"
	"net/http"
	"strings"
)

// refuse(true) makes every write of an object fail with 500, as an API
// server does when it cannot reach etcd. refuse(false) ends it.
func (a *fakeAPI) refuse(refusing bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refusing = refusing
}

// refuseReads(true) makes every GET of a single object fail with 500.
// A list or a watch still succeeds. refuseReads(false) ends it.
func (a *fakeAPI) refuseReads(refusing bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refusingReads = refusing
}

// writes returns each non-GET request to a collection, as "METHOD
// path", in the order the fake received them.
func (a *fakeAPI) writes(path string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, request := range a.requests {
		method, target, _ := strings.Cut(request, " ")
		match := objectPath.FindStringSubmatch(target)
		if match != nil && match[1]+"/"+match[3] == path && method != http.MethodGet {
			out = append(out, request)
		}
	}
	return out
}

// objectOf returns one stored object decoded into T, and whether the
// fake has it.
func objectOf[T any](t interface{ Fatalf(string, ...any) }, a *fakeAPI, path, namespace, name string) (T, bool) {
	var out T
	stored := a.get(path, namespace, name)
	if stored == nil {
		return out, false
	}
	raw, _ := json.Marshal(stored)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding %s %s/%s: %v", path, namespace, name, err)
	}
	return out, true
}
