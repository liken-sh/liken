package main

// These tests cover the merge patch a Sink's status write sends: that
// it turns the published facts into the composed ones, and that it
// leaves the media operator's status.session in place.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// applyMergePatch applies a JSON merge patch to a decoded document, as
// RFC 7386 states it: null removes a field, an object merges field by
// field, and every other value replaces the target whole.
func applyMergePatch(target, patch any) any {
	fields, isObject := patch.(map[string]any)
	if !isObject {
		return patch
	}
	merged, _ := target.(map[string]any)
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range fields {
		if value == nil {
			delete(merged, key)
			continue
		}
		merged[key] = applyMergePatch(merged[key], value)
	}
	return merged
}

// remarshal decodes a document into the operator's struct for it.
func remarshal(fields map[string]any, into any) error {
	encoded, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, into)
}

// The patch, applied to the published facts, gives the composed facts
// exactly: a field that changed, a field that is new, a field that is
// gone, a key gone from a map, and a list that changed.
func TestAStatusPatchTurnsThePublishedFactsIntoTheComposed(t *testing.T) {
	published := EndpointStatus{
		Node:     "liken-1",
		NodeName: sinkNodeName(0, 0),
		Claim:    &EndpointClaim{Namespace: "media", Name: "film"},
		Observed: &EndpointObserved{Volume: pointerTo(40), Controls: map[string]string{
			"Master Playback Volume": "64", "Auto-Mute Mode": "Enabled",
		}},
		Layout: []string{"FL", "FR"},
	}
	composed := EndpointStatus{
		Node:     "liken-1",
		NodeName: sinkNodeName(0, 0),
		Format:   &EndpointFormat{Rate: 48000, Channels: 2},
		Observed: &EndpointObserved{Volume: pointerTo(25), Controls: map[string]string{
			"Master Playback Volume": "64",
		}},
		Layout: []string{"FL", "FR", "FC", "LFE"},
	}

	encoded, err := statusPatch("7", published, composed)
	if err != nil {
		t.Fatal(err)
	}
	var patch map[string]any
	if err := json.Unmarshal(encoded, &patch); err != nil {
		t.Fatal(err)
	}
	before, err := asFields(published)
	if err != nil {
		t.Fatal(err)
	}
	var got EndpointStatus
	if err := remarshal(applyMergePatch(before, patch["status"]).(map[string]any), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, composed) {
		t.Errorf("the patched facts are %+v, want %+v", got, composed)
	}
	if version := patch["metadata"].(map[string]any)["resourceVersion"]; version != "7" {
		t.Errorf("the patch states the version %v, want the copy's 7", version)
	}
}

// denSession is the block the media operator applies to a Sink while
// the den unit uses it.
func denSession() *SinkSession {
	return &SinkSession{Player: "media/den",
		VolumeAsk: &VolumeAsk{Level: 30, At: "2026-10-04T12:15:25.164Z"}}
}

// A status write states this operator's fields alone, so the session
// the media operator wrote is in place after it.
func TestAStatusWriteLeavesTheSession(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName, ResourceVersion: api.nextVersion()},
		Status:   SinkStatus{EndpointStatus: EndpointStatus{Node: "liken-1"}, Session: denSession()},
	}

	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatal(err)
	}
	status := api.sinks[testAnalogName].Status
	if status.NodeName != sinkNodeName(0, 0) {
		t.Fatalf("the pass wrote no status: %+v", status)
	}
	if !reflect.DeepEqual(status.Session, denSession()) {
		t.Errorf("the session after the write is %+v, want %+v", status.Session, denSession())
	}
}
