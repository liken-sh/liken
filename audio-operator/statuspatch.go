package main

// This operator and media-operator both write a Sink's status.
// media-operator writes status.session by server-side apply, so this
// operator does not replace the whole status, which would delete the
// session. It sends a JSON merge patch (RFC 7386) that names only the
// fields it changes, with null for each field it removes.
//
// It does not use server-side apply, because an apply removes a field
// only when no other field manager owns it. A field such as
// status.claims or a condition can be owned by the manager of an
// earlier update, and would stay after the operator stopped setting it.
//
// The patch includes the resourceVersion of the copy it was computed
// from, so the API server returns 409 Conflict when another writer has
// changed the Sink since.

import (
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/memo"
)

const mergePatchType = "application/merge-patch+json"

// mergeDiff returns the merge patch that turns before into after. It
// sets a changed list whole, because a merge patch cannot edit a list.
func mergeDiff(before, after map[string]any) map[string]any {
	patch := map[string]any{}
	for key := range before {
		if _, kept := after[key]; !kept {
			patch[key] = nil
		}
	}
	for key, value := range after {
		was, had := before[key]
		if had && reflect.DeepEqual(was, value) {
			continue
		}
		wasObject, wasIsObject := was.(map[string]any)
		object, isObject := value.(map[string]any)
		if had && wasIsObject && isObject {
			patch[key] = mergeDiff(wasObject, object)
			continue
		}
		patch[key] = value
	}
	return patch
}

func asFields(value any) (map[string]any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{}
	return fields, json.Unmarshal(encoded, &fields)
}

// statusPatch returns the request body that turns published into
// composed, for the copy at the resourceVersion version.
func statusPatch(version string, published, composed EndpointStatus) ([]byte, error) {
	before, err := asFields(published)
	if err != nil {
		return nil, err
	}
	after, err := asFields(composed)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"metadata": map[string]any{"resourceVersion": version},
		"status":   mergeDiff(before, after),
	})
}

// patchSinkStatus writes the status that apply composes from the
// Sink's status, unless apply returns false. After a 409 or a 404 it
// reads the Sink again, calls apply on the new copy, and tries once
// more. After a successful write, *sink is the API server's copy. A
// true result with a nil error means that a write succeeded. It
// returns apiclient.ErrNotFound when the Sink has been deleted.
func patchSinkStatus(c *apiclient.Client, versions *memo.Versions, sink *Sink,
	apply func(published EndpointStatus) (EndpointStatus, bool)) (bool, error) {
	name := sink.Metadata.Name
	path := sinkPath(name)
	write := func() (bool, error) {
		composed, needed := apply(sink.Status.EndpointStatus)
		if !needed {
			return false, nil
		}
		body, err := statusPatch(sink.Metadata.ResourceVersion, sink.Status.EndpointStatus, composed)
		if err != nil {
			return false, err
		}
		return true, versions.Send(name, func() (string, error) {
			stored := new(Sink)
			if err := c.Request(http.MethodPatch, path+"/status", mergePatchType, body, stored); err != nil {
				return "", err
			}
			*sink = *stored
			return stored.Metadata.ResourceVersion, nil
		})
	}
	wrote, err := write()
	if !apiclient.Stale(err) {
		return wrote && err == nil, err
	}
	current, err := memo.ReadFresh[Sink](c, versions, name, path)
	if err != nil {
		return false, err
	}
	*sink = *current
	return write()
}
