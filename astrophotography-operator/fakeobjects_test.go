package main

// The fake API server's answers about one namespaced object: a read, a
// create, an update of the object or of its status, a merge patch, and
// a delete. An update or a patch that states a resourceVersion other
// than the stored one answers 409, the way the API server does, so a
// test sees the operator's optimistic concurrency at work. Each write
// tells every watch, like a change from put.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

// objectPath matches a namespaced collection or one object in it, with
// its status subresource: the prefix of the group and version, the
// namespace, the resource, the name, and /status.
var objectPath = regexp.MustCompile(`^(/api/v1|/apis/[^/]+/[^/]+)/namespaces/([^/]+)/([^/]+)(?:/([^/]+))?(/status)?$`)

// serveObject answers a request about one namespaced object of a held
// collection, and answers false for any other request.
func (a *fakeAPI) serveObject(w http.ResponseWriter, r *http.Request) bool {
	m := objectPath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		return false
	}
	path, namespace, name, status := m[1]+"/"+m[3], m[2], m[4], m[5] != ""
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, held := a.collections[path]; !held || (name == "") != (r.Method == http.MethodPost) {
		return false
	}
	body, _ := io.ReadAll(r.Body)
	var sent map[string]any
	if len(body) > 0 && json.Unmarshal(body, &sent) != nil {
		answerStatus(w, http.StatusBadRequest, "BadRequest")
		return true
	}
	if r.Method == http.MethodPost {
		metadata, _ := sent["metadata"].(map[string]any)
		name, _ = metadata["name"].(string)
	}
	key := namespace + "/" + name
	stored, exists := a.objects[path][key]
	switch {
	case r.Method == http.MethodPost && exists:
		answerStatus(w, http.StatusConflict, "AlreadyExists")
	case r.Method == http.MethodPost:
		metadata := sent["metadata"].(map[string]any)
		metadata["namespace"], metadata["uid"] = namespace, key
		metadata["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339)
		a.write(w, path, key, sent, "ADDED", http.StatusCreated)
	case !exists:
		answerStatus(w, http.StatusNotFound, "NotFound")
	case r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, stored)
	case r.Method == http.MethodDelete:
		a.store(path, key, stored, "DELETED")
		writeJSON(w, http.StatusOK, stored)
	case stale(sent, stored):
		answerStatus(w, http.StatusConflict, "Conflict")
	case r.Method == http.MethodPut && status:
		next := clone(stored)
		next["status"] = sent["status"]
		a.write(w, path, key, next, "MODIFIED", http.StatusOK)
	case r.Method == http.MethodPut:
		sent["status"] = stored["status"]
		a.write(w, path, key, sent, "MODIFIED", http.StatusOK)
	case r.Method == http.MethodPatch:
		a.write(w, path, key, mergePatch(clone(stored), sent), "MODIFIED", http.StatusOK)
	default:
		answerStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
	return true
}

// write stores an object and answers the stored copy. Callers hold mu.
func (a *fakeAPI) write(w http.ResponseWriter, path, key string, object map[string]any, kind string, code int) {
	c := a.collections[path]
	object["apiVersion"], object["kind"] = c.apiVersion, c.kind
	a.store(path, key, object, kind)
	writeJSON(w, code, a.objects[path][key])
}

// stale reports whether a write states a resourceVersion that is not
// the stored one. A write that states none is not stale.
func stale(sent, stored map[string]any) bool {
	metadata, _ := sent["metadata"].(map[string]any)
	version, stated := metadata["resourceVersion"].(string)
	return stated && version != "" && version != stored["metadata"].(map[string]any)["resourceVersion"]
}

// mergePatch applies a JSON merge patch (RFC 7386).
func mergePatch(target, patch map[string]any) map[string]any {
	for key, value := range patch {
		switch value := value.(type) {
		case nil:
			delete(target, key)
		case map[string]any:
			held, _ := target[key].(map[string]any)
			if held == nil {
				held = map[string]any{}
			}
			target[key] = mergePatch(held, value)
		default:
			target[key] = value
		}
	}
	return target
}

func writeJSON(w http.ResponseWriter, code int, object any) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(object)
}

func answerStatus(w http.ResponseWriter, code int, reason string) {
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":%q,"code":%d}`, reason, code)
}

// get answers the stored copy of one object, or nil.
func (a *fakeAPI) get(path, namespace, name string) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	held, exists := a.objects[path][namespace+"/"+name]
	if !exists {
		return nil
	}
	return clone(held)
}

// decode answers the stored copy of one object as a Go value.
func decode[T any](a *fakeAPI, path, namespace, name string) T {
	var out T
	body, _ := json.Marshal(a.get(path, namespace, name))
	_ = json.Unmarshal(body, &out)
	return out
}

// names answers the names of every object of a collection in a
// namespace.
func (a *fakeAPI) names(path, namespace string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, object := range a.objects[path] {
		metadata := object["metadata"].(map[string]any)
		if metadata["namespace"] == namespace {
			out = append(out, metadata["name"].(string))
		}
	}
	return out
}
