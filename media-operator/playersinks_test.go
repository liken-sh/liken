package main

import (
	"reflect"
	"testing"
)

// The two sinks the fixture Player plays through. The first is a wired
// endpoint under the audio operator's inventory name. The second is a
// Bluetooth speaker, whose device name, and so whose Sink name, is its
// address in lowercase dashed form.
const (
	testWiredSink   = "hdmi-0-pch"
	testSpeakerSink = "aa-bb-cc-dd-ee-ff"
)

// A Player with two sinks, the case status.sinks exists for: one video
// track and two audio tracks, and a client with no way to learn the
// second Sink's name from the spec alone.
func twoSinkPlayer() *Player {
	player := housePlayer()
	player.Spec.Sinks = []PlayerDevice{
		{Class: "audio-sink"},
		{Class: "audio-speaker"},
	}
	return player
}

// The playback claim as the scheduler left it: the claim the Player
// produced, with one allocation result per request the caller states.
// The results arrive in the order given, so a test can hand them over
// out of spec order.
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

// A pass writes the list a running Play's allocated claim resolved,
// in spec order, on the Player's status.
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

// The list is memory. It stands after the Play, its claim, and its pod
// go, the way status.screen does, while the Player reads idle.
func TestTheSinksStandAfterThePlayRetires(t *testing.T) {
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

// A Player that states no sink remembers none, so a memory left by an
// earlier spec does not outlive the selection that produced it.
func TestAPlayerWithNoSinkRemembersNone(t *testing.T) {
	cluster := newFakeCluster()
	player := housePlayer()
	player.Spec.Sinks = nil
	player.Status.Sinks = []PlayerSinkStatus{{Request: audioRequestPrefix + "0", Name: testWiredSink}}
	cluster.players["theater"] = player
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	mustMatch(t, len(cluster.players["theater"].Status.Sinks), 0)
}
