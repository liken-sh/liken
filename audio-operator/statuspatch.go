package main

// A Sink's status has two writers. This operator writes the endpoint's
// facts, and the media operator writes status.session by server-side
// apply under its own field manager. A write that replaced the whole
// status would remove the session, so this operator writes a Sink's
// status as a JSON merge patch (RFC 7386) that states only the fields
// it changes, and null for each field it removes.
//
// A merge patch and not a server-side apply, because the Sinks this
// operator wrote with an update hold their status fields under that
// update's manager. An apply removes a field only when no other manager
// owns it, so a status.claim or a condition that an update wrote would
// stay after the apply stopped stating it. A merge patch removes a
// field by naming it.
//
// The patch carries the resourceVersion of the copy it was computed
// from, so the API server refuses it with 409 Conflict when another
// writer changed the Sink since. The caller then reads the Sink again
// and computes the patch from the fresh copy, the way a status update
// does (memo.SettleStatus).

import (
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/memo"
)

const mergePatchType = "application/merge-patch+json"

// mergeDiff answers the merge patch that turns before into after. A
// field after holds and before does not, or holds with another value,
// is stated. A field before holds and after does not is null. An
// object in both is compared field by field, and every other value,
// a list included, is stated whole, because a merge patch replaces a
// list whole.
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

// asFields answers a value as the JSON object it encodes to.
func asFields(value any) (map[string]any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{}
	return fields, json.Unmarshal(encoded, &fields)
}

// statusPatch answers the merge patch that turns the published facts
// into the composed ones, on the copy of the given resourceVersion.
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

// patchSinkStatus writes the facts that apply composes from the
// Sink's copy, when they differ from the copy's. A copy older than the
// API server's is read again and composed again once, and the written
// copy replaces the caller's. It reports whether a write landed, and a
// Sink the API server no longer holds answers apiclient.ErrNotFound.
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
