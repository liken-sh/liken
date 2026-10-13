package main

// What the tests of the solvers and the workers read of the fake API
// server's writes (fakeobjects_test.go): each write to a collection,
// one object as a Go value, and an API server that refuses every write
// for a while.

import (
	"encoding/json"
	"net/http"
	"strings"
)

// refuse makes the writes fail with 500, the way an API server that
// cannot reach etcd answers, or succeed again.
func (a *fakeAPI) refuse(refusing bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refusing = refusing
}

// writes answers each request that wrote to a collection, as its
// method and path, in order.
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

// objectOf answers the collection's copy of one object, decoded into T,
// and whether the collection holds it.
func objectOf[T any](t interface{ Fatalf(string, ...any) }, a *fakeAPI, path, namespace, name string) (T, bool) {
	var out T
	held := a.get(path, namespace, name)
	if held == nil {
		return out, false
	}
	raw, _ := json.Marshal(held)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding %s %s/%s: %v", path, namespace, name, err)
	}
	return out, true
}
