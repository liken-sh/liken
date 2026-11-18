package main

// A change to a field that the pass reads wakes the pass. A change to any
// other field, and a write by this operator, does not.

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/informer"
	"k8s.io/client-go/tools/cache"
)

func allocatedClaim() *ResourceClaim {
	claim := buildClaim(housePlay("https://nas/film.mkv"), housePlayer())
	claim.Metadata.UID = "claim-uid"
	claim.Status = &ResourceClaimStatus{Allocation: &DeviceAllocationResult{
		Devices: DeviceAllocationDevices{Results: []DeviceRequestAllocationResult{
			{Request: "screen", Driver: "display.liken.sh", Pool: "node-5", Device: "card0-dp-1"},
		}},
	}}
	return claim
}

func TestAClaimWakesThePassWhenItsAllocationChanges(t *testing.T) {
	unallocated := allocatedClaim()
	unallocated.Status = nil
	reserved := allocatedClaim()
	reserved.Status.ReservedFor = []ClaimConsumer{{Resource: podsResource, Name: "movie-playback"}}
	deleting := allocatedClaim()
	deleting.Metadata.DeletionTimestamp = "2026-09-09T10:30:00Z"
	someoneElses := allocatedClaim()
	someoneElses.Metadata.OwnerReferences = []OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "app"}}
	reallocated := allocatedClaim()
	reallocated.Status.Allocation.Devices.Results[0].Device = "card0-hdmi-1"
	someoneElsesReallocated := allocatedClaim()
	someoneElsesReallocated.Metadata.OwnerReferences = someoneElses.Metadata.OwnerReferences
	someoneElsesReallocated.Status.Allocation.Devices.Results[0].Device = "card0-hdmi-1"

	cases := []struct {
		name   string
		before *ResourceClaim
		after  *ResourceClaim
		want   bool
	}{
		{name: "the operator creates a claim", after: unallocated, want: false},
		{name: "the watch reads an allocated claim", after: allocatedClaim(), want: true},
		{name: "the scheduler allocates the claim", before: unallocated, after: allocatedClaim(), want: true},
		{name: "the scheduler reallocates the claim", before: allocatedClaim(), after: reallocated, want: true},
		{name: "a pod reserves the claim", before: allocatedClaim(), after: reserved, want: false},
		{name: "a delete begins", before: allocatedClaim(), after: deleting, want: true},
		{name: "another program's claim moves", before: someoneElses, after: someoneElsesReallocated, want: false},
		{name: "another program's claim arrives", after: someoneElses, want: false},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			wake := make(chan struct{}, 1)
			handler := claimRule.handler(wake)

			handChange(t, handler, each.before, each.after)

			mustMatch(t, len(wake) == 1, each.want)
		})
	}
}

// handChange sends an add event when before is nil, and an update event
// otherwise.
func handChange[T any](t *testing.T, handler cache.ResourceEventHandler, before, after *T) {
	t.Helper()
	if before == nil {
		handler.OnAdd(asObject(t, after), false)
		return
	}
	handler.OnUpdate(asObject(t, before), asObject(t, after))
}

func TestAClaimRemovalWakesThePassOnlyForTheOperatorsClaims(t *testing.T) {
	someoneElses := allocatedClaim()
	someoneElses.Metadata.OwnerReferences = nil
	cases := []struct {
		name   string
		object any
		want   bool
	}{
		{name: "this operator's claim", object: asObject(t, allocatedClaim()), want: true},
		{name: "another program's claim", object: asObject(t, someoneElses), want: false},
		{name: "a tombstone with no copy", object: cache.DeletedFinalStateUnknown{Key: "house/movie-devices"}, want: true},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			wake := make(chan struct{}, 1)

			claimRule.handler(wake).OnDelete(each.object)

			mustMatch(t, len(wake) == 1, each.want)
		})
	}
}

func TestASliceWakesThePassWhenItsSpecChanges(t *testing.T) {
	published := monitorSlice()
	published.Metadata.UID, published.Metadata.Generation = "slice-uid", 1
	republished := published
	republished.Metadata.Generation = 2
	relabeled := published
	relabeled.Metadata.Labels = map[string]string{"team": "screens"}

	cases := []struct {
		name   string
		before *ResourceSlice
		after  *ResourceSlice
		want   bool
	}{
		{name: "a driver publishes a slice", after: &published, want: true},
		{name: "a driver changes its devices", before: &published, after: &republished, want: true},
		{name: "a label changes", before: &published, after: &relabeled, want: false},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			wake := make(chan struct{}, 1)

			handChange(t, sliceRule.handler(wake), each.before, each.after)

			mustMatch(t, len(wake) == 1, each.want)
		})
	}
}

func TestADisplayWakesThePassWhenItsStatusChanges(t *testing.T) {
	lit := litDisplay()
	overridden := litDisplay()
	overridden.Spec.Override = &DisplayOverride{Backlight: displayPowerOff}
	dimmed := litDisplay()
	dimmed.Status.Observed.Brightness = ptr(0)

	cases := []struct {
		name   string
		before *Display
		after  *Display
		want   bool
	}{
		{name: "the display-operator publishes a Display", after: lit, want: true},
		{name: "this operator applies the override", before: lit, after: overridden, want: false},
		{name: "the panel reports a new brightness", before: overridden, after: dimmed, want: true},
		{name: "the panel leaves", before: lit, after: awayDisplay(), want: true},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			wake := make(chan struct{}, 1)

			handChange(t, displayRule.handler(wake), each.before, each.after)

			mustMatch(t, len(wake) == 1, each.want)
		})
	}
}

func TestAReceiverWakesThePassWhenTheEquipmentOrItsWiringChanges(t *testing.T) {
	baseline := houseReceiver()
	sessioned := houseReceiver()
	sessioned.Status.Session = &ReceiverSession{Player: "house/theater", Input: "GAME", Active: true}
	specSessioned := houseReceiver()
	specSessioned.Spec.Session = &ReceiverSession{Player: "house/theater", Input: "GAME"}
	switched := houseReceiver()
	switched.Status.Input = "CBL/SAT"
	rewired := houseReceiver()
	rewired.Spec.Inputs = rewired.Spec.Inputs[:1]
	unreachable := houseReceiver()
	unreachable.Status.Conditions[0].Status = "False"
	newLevel := houseReceiver()
	newLevel.Status.Zones = map[string]ReceiverZone{mainZone: {Volume: "46.5"}}
	rescaled := houseReceiver()
	rescaled.Spec.Volume = &ReceiverVolume{Max: 60}
	withVolumeAsk := houseReceiver()
	withVolumeAsk.Status.Session = &ReceiverSession{Player: "house/theater", Input: "GAME",
		VolumeAsk: &VolumeAsk{Level: 45, At: "2026-10-04T12:15:25.164Z"}}

	cases := []struct {
		name   string
		before *Receiver
		after  *Receiver
		want   bool
	}{
		{name: "this operator applies the status session", before: baseline, after: sessioned, want: false},
		{name: "this operator releases the spec session", before: specSessioned, after: baseline, want: false},
		{name: "the receiver changes input", before: baseline, after: switched, want: true},
		{name: "a person rewires the inputs", before: baseline, after: rewired, want: true},
		{name: "the receiver stops answering", before: baseline, after: unreachable, want: true},
		{name: "the receiver reports a level", before: baseline, after: newLevel, want: true},
		{name: "a person changes the volume scale", before: baseline, after: rescaled, want: true},
		{name: "this operator writes a volume ask", before: baseline, after: withVolumeAsk, want: false},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			wake := make(chan struct{}, 1)

			handChange(t, receiverRule.handler(wake), each.before, each.after)

			mustMatch(t, len(wake) == 1, each.want)
		})
	}
}

func TestASinkWakesThePassWhenItsScaleOrItsReportChanges(t *testing.T) {
	baseline := frontSink()
	reported := frontSink()
	level := 45
	reported.Status.Observed.Volume = &level
	rescaled := frontSink()
	maxPercent := 80
	rescaled.Spec.Volume = &SinkVolume{Max: &maxPercent}
	withVolumeAsk := frontSink()
	withVolumeAsk.Status.Session = &SinkSession{Player: "house/theater",
		VolumeAsk: &SinkVolumeAsk{Level: 45, At: "2026-10-04T12:15:25.164Z"}}

	cases := []struct {
		name   string
		before *Sink
		after  *Sink
		want   bool
	}{
		{name: "the sink reports a level", before: baseline, after: reported, want: true},
		{name: "a person changes the volume scale", before: baseline, after: rescaled, want: true},
		{name: "this operator writes a volume ask", before: baseline, after: withVolumeAsk, want: false},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			wake := make(chan struct{}, 1)

			handChange(t, sinkRule.handler(wake), each.before, each.after)

			mustMatch(t, len(wake) == 1, each.want)
		})
	}
}

func TestATelevisionWakesThePassForANewScreenAsk(t *testing.T) {
	cases := []struct {
		name   string
		before *Television
		after  *Television
		want   bool
	}{
		{name: "a new ask", before: screenAskOn("2026-10-04T12:00:00.000Z", "Wake"),
			after: screenAskOn("2026-10-04T12:05:00.000Z", "Wake"), want: true},
		{name: "the same ask", before: screenAskOn("2026-10-04T12:00:00.000Z", "Wake"),
			after: screenAskOn("2026-10-04T12:00:00.000Z", "Wake"), want: false},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			wake := make(chan struct{}, 1)

			handChange(t, televisionRule.handler(wake), each.before, each.after)

			mustMatch(t, len(wake) == 1, each.want)
		})
	}
}

// The same rule holds for events that come through client-go's reflector.
func TestADisplayWakesThePassThroughTheReflector(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newCollectionServer()
		displays := server.serve(t, displayResource, "Display", litDisplay())
		wake := make(chan struct{}, 1)
		synced := make(chan struct{})
		runWatch(t, server, collectionWatch{resource: displayResource, handler: displayRule.handler(wake),
			synced: func(informer.View) { close(synced) }})
		synctest.Wait()
		mustMatch(t, received(synced), true)
		mustMatch(t, received(wake), true)

		overridden := litDisplay()
		overridden.Metadata.ResourceVersion = "101"
		overridden.Spec.Override = &DisplayOverride{Backlight: displayPowerOff}
		displays.send(t, "MODIFIED", overridden)
		synctest.Wait()
		mustMatch(t, received(wake), false)

		away := awayDisplay()
		away.Metadata.ResourceVersion = "102"
		displays.send(t, "MODIFIED", away)
		synctest.Wait()
		mustMatch(t, received(wake), true)
	})
}
