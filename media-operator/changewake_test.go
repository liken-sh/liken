package main

// These tests cover the wake rules of the claims, the ResourceSlices,
// the Displays, and the Receivers: a change to a field the pass reads
// wakes it, and this operator's own write and every other change do
// not.

import (
	"testing"

	"github.com/liken-sh/liken/kubernetes/informer"
	"k8s.io/client-go/tools/cache"
)

// allocatedClaim is a claim of the house Play with its playback device
// allocated.
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

// handChange hands one change to a handler: an addition when before is
// nil, and an update otherwise.
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
	held := houseReceiver()
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

	cases := []struct {
		name   string
		before *Receiver
		after  *Receiver
		want   bool
	}{
		{name: "this operator applies the status session", before: held, after: sessioned, want: false},
		{name: "this operator releases the spec session", before: specSessioned, after: held, want: false},
		{name: "the receiver changes input", before: held, after: switched, want: true},
		{name: "a person rewires the inputs", before: held, after: rewired, want: true},
		{name: "the receiver stops answering", before: held, after: unreachable, want: true},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			wake := make(chan struct{}, 1)

			handChange(t, receiverRule.handler(wake), each.before, each.after)

			mustMatch(t, len(wake) == 1, each.want)
		})
	}
}

// Through the reflector: a Display the display-operator rewrites wakes
// the pass, and the override this operator applies does not.
func TestADisplayWakesThePassThroughTheReflector(t *testing.T) {
	server := newCollectionServer()
	displays := server.serve(t, displayResource, "Display", litDisplay())
	wake := make(chan struct{}, 1)
	synced := make(chan struct{})
	runWatch(t, server, collectionWatch{resource: displayResource, handler: displayRule.handler(wake),
		synced: func(informer.View) { close(synced) }})
	mustMatch(t, closedWithin(synced, watchTimeout), true)
	<-wake

	overridden := litDisplay()
	overridden.Metadata.ResourceVersion = "101"
	overridden.Spec.Override = &DisplayOverride{Backlight: displayPowerOff}
	displays.send(t, "MODIFIED", overridden)
	mustMatch(t, wokeWithin(wake), false)

	away := awayDisplay()
	away.Metadata.ResourceVersion = "102"
	displays.send(t, "MODIFIED", away)
	mustMatch(t, wokeWithin(wake), true)
}
