package main

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

// sharedClaim returns an allocated ResourceClaim that requests one unit
// of the speaker's claims capacity. That capacity request makes the
// claim a share of the speaker.
func sharedClaim(name, uid, config string) string {
	return fmt.Sprintf(`{
	  "metadata": {"name": %q, "namespace": "media", "uid": %q},
	  "spec": {"devices": {"requests": [
	    {"name": "speaker", "exactly": {"deviceClassName": "speaker", "capacity": {"requests": {"claims": "1"}}}}
	  ]}},
	  "status": {"allocation": {"devices": {
	    "results": [
	      {"request": "speaker", "driver": "audio.liken.sh", "pool": "liken-1", "device": %q,
	       "shareID": "8a0b6f8e-0000-4000-8000-%012d", "consumedCapacity": {"claims": "1"}}
	    ],
	    "config": [%s]
	  }}}
	}`, name, uid, testSpeakerName, len(name), config)
}

// claimsPlugin returns a DRA plugin whose fake API server serves these
// claims by name.
func claimsPlugin(t *testing.T, pipewire *fakePipeWire, claims map[string]string) *draPlugin {
	t.Helper()
	plugin := codecPlugin(t, "", pipewire)
	plugin.client = testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		document, found := claims[name]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(document))
	}))
	plugin.claims = &preparedClaims{}
	return plugin
}

func prepareNamed(t *testing.T, plugin *draPlugin, name, uid string) *drav1.NodePrepareResourceResponse {
	t.Helper()
	resp, err := plugin.NodePrepareResources(context.Background(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: name, Uid: uid}},
	})
	if err != nil {
		t.Fatalf("preparing the claim %s: %v", name, err)
	}
	return resp.Claims[uid]
}

// The second claim on a shared speaker plays through the sink that
// the first claim already uses. Only the first prepare sets the volume
// to unity, because a write by the second would change the level of
// the stream that is already playing.
func TestASecondShareLeavesTheLevelAlone(t *testing.T) {
	specDirectory(t)
	pipewire := &fakePipeWire{sink: currentSink()}
	plugin := claimsPlugin(t, pipewire, map[string]string{
		"kitchen":  sharedClaim("kitchen", "claim-1", ""),
		"doorbell": sharedClaim("doorbell", "claim-2", ""),
	})

	for _, claim := range []struct{ name, uid string }{{"kitchen", "claim-1"}, {"doorbell", "claim-2"}} {
		if entry := prepareNamed(t, plugin, claim.name, claim.uid); entry.Error != "" {
			t.Fatalf("preparing the claim %s: %s", claim.name, entry.Error)
		}
	}
	if len(pipewire.volumeWrites) != 1 {
		t.Errorf("volume writes = %+v, want the first prepare's alone", pipewire.volumeWrites)
	}
	want := []EndpointClaim{{Namespace: "media", Name: "doorbell"}, {Namespace: "media", Name: "kitchen"}}
	if got := plugin.claims.holders(testSpeakerName); !reflect.DeepEqual(got, want) {
		t.Errorf("holders = %+v, want %+v", got, want)
	}
}

// The kubelet can prepare a claim again. heldByAnother ignores the
// claim's own UID, so the repeat writes unity again.
func TestARepeatedPrepareOfTheOnlyShareWritesUnity(t *testing.T) {
	specDirectory(t)
	pipewire := &fakePipeWire{sink: currentSink()}
	plugin := claimsPlugin(t, pipewire, map[string]string{"kitchen": sharedClaim("kitchen", "claim-1", "")})

	for range 2 {
		if entry := prepareNamed(t, plugin, "kitchen", "claim-1"); entry.Error != "" {
			t.Fatalf("preparing the claim: %s", entry.Error)
		}
	}
	if len(pipewire.volumeWrites) != 2 {
		t.Errorf("volume writes = %+v, want one for each prepare", pipewire.volumeWrites)
	}
}

// A codec switch rebuilds the speaker's sink, which interrupts every
// other claim that plays through it. So prepare refuses a shared claim
// that selects a codec, and writes no codec and no spec file.
func TestPrepareRefusesACodecOnASharedClaim(t *testing.T) {
	cases := []struct {
		name   string
		config string
	}{
		{name: "the claim's own block", config: claimEntry(`{"codec": "aptx"}`)},
		{name: "the DeviceClass's block", config: classEntry(`{"codec": "aptx"}`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := specDirectory(t)
			pipewire := &fakePipeWire{sink: currentSink()}
			plugin := claimsPlugin(t, pipewire, map[string]string{"kitchen": sharedClaim("kitchen", "claim-1", c.config)})

			entry := prepareNamed(t, plugin, "kitchen", "claim-1")
			want := "the claim shares " + testSpeakerName + " and selects the codec aptx"
			if !strings.Contains(entry.Error, want) {
				t.Errorf("error = %q, want it to contain %q", entry.Error, want)
			}
			if len(pipewire.codecWrites) != 0 {
				t.Errorf("codec writes = %+v, want none", pipewire.codecWrites)
			}
			if left := specFiles(t, dir); len(left) != 0 {
				t.Errorf("a refused claim left the spec files %v, want none", left)
			}
		})
	}
}

// When a request is a firstAvailable list, the allocation result names
// the chosen alternative as request/subrequest. The capacity of that
// subrequest decides whether the claim is a share.
func TestASubrequestThatAsksForAShareIsAShare(t *testing.T) {
	specDirectory(t)
	pipewire := &fakePipeWire{sink: currentSink()}
	plugin := claimsPlugin(t, pipewire, map[string]string{"kitchen": fmt.Sprintf(`{
	  "metadata": {"name": "kitchen", "namespace": "media", "uid": "claim-1"},
	  "spec": {"devices": {"requests": [
	    {"name": "sound", "firstAvailable": [
	      {"name": "speaker", "deviceClassName": "speaker", "capacity": {"requests": {"claims": "1"}}},
	      {"name": "jack", "deviceClassName": "jack"}
	    ]}
	  ]}},
	  "status": {"allocation": {"devices": {
	    "results": [{"request": "sound/speaker", "driver": "audio.liken.sh", "pool": "liken-1", "device": %q}],
	    "config": [%s]
	  }}}
	}`, testSpeakerName, claimEntry(`{"codec": "aptx"}`))})

	entry := prepareNamed(t, plugin, "kitchen", "claim-1")
	if !strings.Contains(entry.Error, "the claim shares") {
		t.Errorf("error = %q, want the refusal of a shared claim's codec", entry.Error)
	}
}

// Unpreparing one share releases only that claim. The other claim
// still holds the speaker.
func TestUnprepareReleasesOneShare(t *testing.T) {
	specDirectory(t)
	pipewire := &fakePipeWire{sink: currentSink()}
	plugin := claimsPlugin(t, pipewire, map[string]string{
		"kitchen":  sharedClaim("kitchen", "claim-1", ""),
		"doorbell": sharedClaim("doorbell", "claim-2", ""),
	})
	prepareNamed(t, plugin, "kitchen", "claim-1")
	prepareNamed(t, plugin, "doorbell", "claim-2")

	if _, err := plugin.NodeUnprepareResources(context.Background(), &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "kitchen", Uid: "claim-1"}},
	}); err != nil {
		t.Fatalf("unpreparing the claim kitchen: %v", err)
	}
	want := []EndpointClaim{{Namespace: "media", Name: "doorbell"}}
	if got := plugin.claims.holders(testSpeakerName); !reflect.DeepEqual(got, want) {
		t.Errorf("holders = %+v, want %+v", got, want)
	}
}
