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

// The store's copy of a Player can be older than the operator's own
// status write. A pass that carried the Sinks forward from that copy
// would write the older memory back, and the unit would forget the Sinks
// its last run resolved. The pass reads the Player from the API server
// instead, and keeps them.
func TestAPassKeepsThePlayerMemoryItWroteOverAnOlderCopy(t *testing.T) {
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

	// The run is over, so no claim says which Sinks the unit plays
	// through, and the watch has not delivered the Player write.
	delete(cluster.plays, "movie")
	delete(cluster.pods, podName("movie"))
	media.view.players.view.store = runningCluster(twoSinkPlayer()).view().players.view.store
	media.pass()

	if got := cluster.players["theater"].Status.Sinks; !reflect.DeepEqual(got, want) {
		t.Errorf("sinks = %+v, want the memory %+v", got, want)
	}
}

// A Player whose spec a person edited since the pass read it answers the
// status write with 409. The write reads the Player again and writes
// the status onto the fresh copy, and the spec edit stands.
func TestAPlayerStatusWriteAfterASpecEditLandsOnTheFreshCopy(t *testing.T) {
	cluster := newFakeCluster()
	read := twoSinkPlayer()
	read.Metadata.ResourceVersion = "4"
	edited := twoSinkPlayer()
	edited.Metadata.ResourceVersion = "5"
	edited.Spec.Zone = "den"
	cluster.players["theater"] = edited
	desired := PlayerStatus{Activity: playerIdle, Sinks: []PlayerSinkStatus{{Request: audioRequestPrefix + "0", Name: testWiredSink}}}

	mustSucceed(t, writePlayerStatus(testAPIClient(t, cluster.handler(t)), newVersionMemo(), read, desired))

	mustMatch(t, reflect.DeepEqual(cluster.players["theater"].Status, desired), true)
	mustMatch(t, cluster.players["theater"].Spec.Zone, "den")
	mustMatch(t, countMethod(cluster.requests, "PUT"), 2)
}
