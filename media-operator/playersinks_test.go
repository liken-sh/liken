package main

import (
	"reflect"
	"testing"

	"github.com/liken-sh/liken/kubernetes/memo"
)

// A wired endpoint and a Bluetooth speaker. A Bluetooth speaker's Sink
// name is its address in lowercase with dashes.
const (
	testWiredSink   = "hdmi-0-pch"
	testSpeakerSink = "aa-bb-cc-dd-ee-ff"
)

func twoSinkPlayer() *Player {
	player := housePlayer()
	player.Spec.Sinks = []PlayerDevice{
		{Class: "audio-sink"},
		{Class: "audio-speaker"},
	}
	return player
}

// allocatedPlaybackClaim returns the Play's claim with the given
// allocation results, in the given order, so a test can put them out
// of spec order.
func allocatedPlaybackClaim(results ...DeviceRequestAllocationResult) *ResourceClaim {
	claim := buildClaim(housePlay("https://nas/film.mkv"), twoSinkPlayer())
	claim.Status = &ResourceClaimStatus{
		Allocation: &DeviceAllocationResult{
			Devices: DeviceAllocationDevices{Results: results},
		},
	}
	return claim
}

func audioResult(request, device string) DeviceRequestAllocationResult {
	return DeviceRequestAllocationResult{
		Request: request,
		Driver:  audioDriver,
		Pool:    testNode,
		Device:  device,
	}
}

func TestSinksReadTheAllocationInSpecOrder(t *testing.T) {
	claim := allocatedPlaybackClaim(
		audioResult(audioRequestPrefix+"1", testSpeakerSink),
		audioResult(audioRequestPrefix+"0", testWiredSink),
		DeviceRequestAllocationResult{Request: screenRequest, Driver: "display.liken.sh", Device: "card0-dp-1"},
	)

	got := sinksOf(claim, 2)

	want := []PlayerSinkStatus{
		{Request: audioRequestPrefix + "0", Name: testWiredSink},
		{Request: audioRequestPrefix + "1", Name: testSpeakerSink},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sinks = %+v, want %+v", got, want)
	}
}

func TestSinksSkipARequestNoAudioDriverAllocated(t *testing.T) {
	claim := allocatedPlaybackClaim(
		audioResult(audioRequestPrefix+"0", testWiredSink),
		DeviceRequestAllocationResult{
			Request: audioRequestPrefix + "1",
			Driver:  "bluetooth.liken.sh",
			Device:  testSpeakerSink,
		},
	)

	got := sinksOf(claim, 2)

	want := []PlayerSinkStatus{{Request: audioRequestPrefix + "0", Name: testWiredSink}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sinks = %+v, want %+v", got, want)
	}
}

func TestSinksAreEmptyUntilTheClaimIsAllocated(t *testing.T) {
	claim := buildClaim(housePlay("https://nas/film.mkv"), twoSinkPlayer())

	mustMatch(t, len(sinksOf(claim, 2)), 0)
}

func TestAPassWritesTheSinksTheClaimResolved(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = twoSinkPlayer()
	cluster.pods["movie-playback"] = housePlaybackPod()
	cluster.claims["movie-devices"] = allocatedPlaybackClaim(
		audioResult(audioRequestPrefix+"0", testWiredSink),
		audioResult(audioRequestPrefix+"1", testSpeakerSink),
	)
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	got := cluster.players["theater"].Status.Sinks
	want := []PlayerSinkStatus{
		{Request: audioRequestPrefix + "0", Name: testWiredSink},
		{Request: audioRequestPrefix + "1", Name: testSpeakerSink},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sinks = %+v, want %+v", got, want)
	}
}

func TestTheSinksRemainAfterThePlayRetires(t *testing.T) {
	cluster := newFakeCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	cluster.players["theater"] = twoSinkPlayer()
	cluster.pods["movie-playback"] = housePlaybackPod()
	cluster.claims["movie-devices"] = allocatedPlaybackClaim(
		audioResult(audioRequestPrefix+"0", testWiredSink),
	)
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.pass()

	delete(cluster.plays, "movie")
	delete(cluster.claims, "movie-devices")
	delete(cluster.pods, "movie-playback")
	media.pass()

	got := cluster.players["theater"].Status
	mustMatch(t, got.Activity, playerIdle)
	want := []PlayerSinkStatus{{Request: audioRequestPrefix + "0", Name: testWiredSink}}
	if !reflect.DeepEqual(got.Sinks, want) {
		t.Errorf("sinks = %+v, want %+v", got.Sinks, want)
	}
}

func TestAPlayerWithNoSinkSelectionHasNoSinkStatus(t *testing.T) {
	cluster := newFakeCluster()
	player := housePlayer()
	player.Spec.Sinks = nil
	player.Status.Sinks = []PlayerSinkStatus{{Request: audioRequestPrefix + "0", Name: testWiredSink}}
	cluster.players["theater"] = player
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	mustMatch(t, len(cluster.players["theater"].Status.Sinks), 0)
}

// The store's copy of a Player can be older than the operator's last
// status write. A pass that kept the Sinks of that copy would write
// back the older list. The pass reads a Player from the API server when
// its store copy is older than the last write, so it keeps the Sinks.
func TestAPassKeepsTheSinksItWroteOverAnOlderStoreCopy(t *testing.T) {
	cluster := runningCluster(twoSinkPlayer())
	cluster.claims[claimName("movie")] = allocatedPlaybackClaim(
		audioResult(audioRequestPrefix+"0", testWiredSink),
		audioResult(audioRequestPrefix+"1", testSpeakerSink),
	)
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.pass()
	want := []PlayerSinkStatus{
		{Request: audioRequestPrefix + "0", Name: testWiredSink},
		{Request: audioRequestPrefix + "1", Name: testSpeakerSink},
	}
	mustMatch(t, reflect.DeepEqual(cluster.players["theater"].Status.Sinks, want), true)

	// The Play is gone, and the store has not yet received the status
	// write.
	delete(cluster.plays, "movie")
	delete(cluster.pods, podName("movie"))
	media.view.players.View.Store = runningCluster(twoSinkPlayer()).view().players.View.Store
	media.pass()

	if got := cluster.players["theater"].Status.Sinks; !reflect.DeepEqual(got, want) {
		t.Errorf("sinks = %+v, want the earlier list %+v", got, want)
	}
}

// When a person edits a Player's spec after the pass read it, the
// status write gets a 409. writePlayerStatus then reads the Player again
// and writes the status onto the current copy, so the edit is kept.
func TestAPlayerStatusWriteAfterASpecEditUsesTheCurrentCopy(t *testing.T) {
	cluster := newFakeCluster()
	read := twoSinkPlayer()
	read.Metadata.ResourceVersion = "4"
	edited := twoSinkPlayer()
	edited.Metadata.ResourceVersion = "5"
	edited.Spec.Zone = "den"
	cluster.players["theater"] = edited
	desired := PlayerStatus{Activity: playerIdle, Sinks: []PlayerSinkStatus{{Request: audioRequestPrefix + "0", Name: testWiredSink}}}

	mustSucceed(t, writePlayerStatus(testAPIClient(t, cluster.handler(t)), memo.New(), read, desired))

	mustMatch(t, reflect.DeepEqual(cluster.players["theater"].Status, desired), true)
	mustMatch(t, cluster.players["theater"].Spec.Zone, "den")
	mustMatch(t, countMethod(cluster.requests, "PUT"), 2)
}
