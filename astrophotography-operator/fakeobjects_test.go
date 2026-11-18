package main

// The fake API server's handling of one namespaced object: read,
// create, update of the object or of its status, merge patch, and
// delete. Like the real API server, a delete of an object with
// finalizers sets deletionTimestamp, and the object is removed when a
// write leaves it with no finalizers. An update or a patch with a
// resourceVersion other than the stored one gets a 409, so a test
// exercises the operator's optimistic concurrency. Each write notifies
// every watch, like a change made with put.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

// objectPath matches the path of a namespaced collection, or of one
// object in it with an optional /status subresource. Its groups are the
// group and version prefix, the namespace, the resource, and the name.
var objectPath = regexp.MustCompile(`^(/api/v1|/apis/[^/]+/[^/]+)/namespaces/([^/]+)/([^/]+)(?:/([^/]+))?(/status)?$`)

// serveObject handles a request about one namespaced object of a
// collection the fake holds. It returns false for any other request.
func (a *fakeAPI) serveObject(w http.ResponseWriter, r *http.Request) bool {
	m := objectPath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		return false
	}
	path, namespace, name, status := m[1]+"/"+m[3], m[2], m[4], m[5] != ""
	if r.Method == http.MethodPut && status && a.refuseStatus(w, r, path, namespace+"/"+name) {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, known := a.collections[path]; !known || (name == "") != (r.Method == http.MethodPost) {
		return false
	}
	if (a.refusing && r.Method != http.MethodGet) || (a.refusingReads && r.Method == http.MethodGet) {
		writeStatus(w, http.StatusInternalServerError, "InternalError")
		return true
	}
	body, _ := io.ReadAll(r.Body)
	var sent map[string]any
	if len(body) > 0 && json.Unmarshal(body, &sent) != nil {
		writeStatus(w, http.StatusBadRequest, "BadRequest")
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
		writeStatus(w, http.StatusConflict, "AlreadyExists")
	case r.Method == http.MethodPost:
		metadata := sent["metadata"].(map[string]any)
		metadata["namespace"], metadata["uid"] = namespace, key
		metadata["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339)
		a.write(w, path, key, sent, "ADDED", http.StatusCreated)
	case !exists:
		writeStatus(w, http.StatusNotFound, "NotFound")
	case r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, stored)
	case r.Method == http.MethodDelete:
		a.deleteLocked(path, key)
		writeJSON(w, http.StatusOK, stored)
	case stale(sent, stored):
		writeStatus(w, http.StatusConflict, "Conflict")
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
		writeStatus(w, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
	return true
}

// write stores an object and responds with the stored copy. An object
// that is being deleted is removed when its last finalizer is removed.
// Callers hold mu.
func (a *fakeAPI) write(w http.ResponseWriter, path, key string, object map[string]any, kind string, code int) {
	c := a.collections[path]
	object["apiVersion"], object["kind"] = c.apiVersion, c.kind
	metadata := object["metadata"].(map[string]any)
	if finalizers, _ := metadata["finalizers"].([]any); metadata["deletionTimestamp"] != nil && len(finalizers) == 0 {
		a.store(path, key, object, "DELETED")
		writeJSON(w, code, object)
		return
	}
	a.store(path, key, object, kind)
	writeJSON(w, code, a.objects[path][key])
}

// deleteLocked deletes a stored object, or sets deletionTimestamp on it
// while it has a finalizer. Callers hold mu.
func (a *fakeAPI) deleteLocked(path, key string) {
	stored := a.objects[path][key]
	metadata := stored["metadata"].(map[string]any)
	if finalizers, _ := metadata["finalizers"].([]any); len(finalizers) == 0 {
		a.store(path, key, stored, "DELETED")
		return
	}
	if metadata["deletionTimestamp"] == nil {
		marked := clone(stored)
		marked["metadata"].(map[string]any)["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339)
		a.store(path, key, marked, "MODIFIED")
	}
}

// delete deletes an object the way kubectl delete does.
func (a *fakeAPI) delete(path, namespace, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.deleteLocked(path, namespace+"/"+name)
}

// stale reports whether a write carries a resourceVersion that differs
// from the stored one. A write with no resourceVersion is not stale.
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
			nested, _ := target[key].(map[string]any)
			if nested == nil {
				nested = map[string]any{}
			}
			target[key] = mergePatch(nested, value)
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

func writeStatus(w http.ResponseWriter, code int, reason string) {
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":%q,"code":%d}`, reason, code)
}

// get returns the stored copy of one object, or nil.
func (a *fakeAPI) get(path, namespace, name string) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	stored, exists := a.objects[path][namespace+"/"+name]
	if !exists {
		return nil
	}
	return clone(stored)
}

// decode returns the stored copy of one object as a Go value.
func decode[T any](a *fakeAPI, path, namespace, name string) T {
	var out T
	body, _ := json.Marshal(a.get(path, namespace, name))
	_ = json.Unmarshal(body, &out)
	return out
}

// names returns the names of every object of a collection in a
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
