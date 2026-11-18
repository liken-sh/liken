package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// applyMergePatch applies a JSON merge patch to a decoded document as
// RFC 7386 defines it: null removes a field, an object merges field by
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

func remarshal(fields map[string]any, into any) error {
	encoded, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, into)
}

// The patch turns the published status into exactly the composed one,
// across a changed field, a new field, a removed field, a removed map
// key, and a changed list.
func TestAStatusPatchTurnsThePublishedFactsIntoTheComposed(t *testing.T) {
	published := EndpointStatus{
		Node:     "liken-1",
		NodeName: sinkNodeName(0, 0),
		Claims:   []EndpointClaim{{Namespace: "media", Name: "film"}},
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
		t.Fatalf("computing the status patch: %v", err)
	}
	var patch map[string]any
	if err := json.Unmarshal(encoded, &patch); err != nil {
		t.Fatalf("decoding the status patch: %v", err)
	}
	before, err := asFields(published)
	if err != nil {
		t.Fatalf("encoding the published facts: %v", err)
	}
	var got EndpointStatus
	if err := remarshal(applyMergePatch(before, patch["status"]).(map[string]any), &got); err != nil {
		t.Fatalf("decoding the patched facts: %v", err)
	}
	if !reflect.DeepEqual(got, composed) {
		t.Errorf("the patched facts are %+v, want %+v", got, composed)
	}
	if version := patch["metadata"].(map[string]any)["resourceVersion"]; version != "7" {
		t.Errorf("the patch names the version %v, want the copy's 7", version)
	}
}

// denSession is the status.session that media-operator applies to a
// Sink while the Player media/den uses it.
func denSession() *SinkSession {
	return &SinkSession{Player: "media/den",
		VolumeAsk: &VolumeAsk{Level: 30, At: "2026-10-04T12:15:25.164Z"}}
}

// A status write leaves the status.session that media-operator wrote.
func TestAStatusWriteLeavesTheSession(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName, ResourceVersion: api.nextVersion()},
		Status:   SinkStatus{EndpointStatus: EndpointStatus{Node: "liken-1"}, Session: denSession()},
	}

	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	status := api.sinks[testAnalogName].Status
	if status.NodeName != sinkNodeName(0, 0) {
		t.Fatalf("the status after the pass is %+v, want the node name %s written", status, sinkNodeName(0, 0))
	}
	if !reflect.DeepEqual(status.Session, denSession()) {
		t.Errorf("the session after the write is %+v, want %+v", status.Session, denSession())
	}
}
