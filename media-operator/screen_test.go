package main

// These tests cover the media layer's half of the Display resource:
// the lookup from an allocated draw device to the Display that names
// the panel, the override each desire writes there, and what a
// cluster with no Display does instead.

import (
	"net/http"
	"testing"
)

// The monitor id the display-operator publishes for the
// screen, which is also the name of its Display.
const testMonitor = "DP-1"

// The idle claim as the scheduler left it: the draw request
// allocated against one device in the display driver's pool.
func allocatedIdleClaim() *ResourceClaim {
	claim := buildIdleClaim(housePlayer(), "display-draw")
	claim.Status = &ResourceClaimStatus{
		Allocation: &DeviceAllocationResult{
			Devices: DeviceAllocationDevices{
				Results: []DeviceRequestAllocationResult{{
					Request: idleDrawRequest,
					Driver:  "display.liken.sh",
					Pool:    "nuc5",
					Device:  "card0-dp-1-draw",
				}},
			},
		},
	}
	return claim
}

// The slice entry that carries the allocated device's
// attributes, the monitor id among them.
func monitorSlice() ResourceSlice {
	return ResourceSlice{
		Metadata: ObjectMeta{Name: "nuc5-display"},
		Spec: ResourceSliceSpec{
			Driver: "display.liken.sh",
			Pool:   ResourceSlicePool{Name: "nuc5"},
			Devices: []ResourceSliceItem{{
				Name:       "card0-dp-1-draw",
				Attributes: map[string]DeviceAttribute{monitorIDAttribute: {String: ptr(testMonitor)}},
			}},
		},
	}
}

// The panel as the display-operator reports it while it is
// lit, with no override standing.
func litDisplay() *Display {
	return &Display{
		Metadata: ObjectMeta{Name: testMonitor},
		Status:   DisplayStatus{Observed: DisplayObserved{Brightness: ptr(70), Power: "on"}},
	}
}

// screenCluster is a cluster whose theater unit has an
// allocated screen and a Display for it.
func screenCluster() *fakeCluster {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayer()
	cluster.claims[idleClaimName("theater")] = allocatedIdleClaim()
	cluster.slices = []ResourceSlice{monitorSlice()}
	cluster.displays[testMonitor] = litDisplay()
	return cluster
}

// statePanel hands the operator one desire off the bus and
// runs the pass that acts on it.
func statePanel(media *operator, players []Player, desire string, defaults *IdlePolicy) {
	media.handleBusMessage(playerPanelTopic(defaultTopicBase, "house", "theater"),
		[]byte(`{"desire":"`+desire+`"}`))
	media.reconcilePlayers(players, nil, "", defaults)
}

// The off desire becomes a backlight override on the screen's
// own Display, applied under this operator's field manager.
func TestTheOffDesireOverridesTheBacklight(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	statePanel(media, []Player{*housePlayer()}, panelDesireOff, nil)

	mustMatch(t, len(cluster.applies), 1)
	mustMatch(t, cluster.applies[0].name, testMonitor)
	mustMatch(t, cluster.applies[0].manager, displayFieldManager)
	mustMatch(t, *cluster.applies[0].override, DisplayOverride{Backlight: displayPowerOff})
	mustMatch(t, *cluster.displays[testMonitor].Spec.Override, DisplayOverride{Backlight: displayPowerOff})
}

// The resolved off mode picks the block. The power mode is
// deeper than the backlight, and a Player states it only for a panel
// a drill proved wakes.
func TestTheOffDesireOverridesThePowerWhenTheModeStatesIt(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	statePanel(media, []Player{*housePlayer()}, panelDesireOff, &IdlePolicy{OffMode: offModePower})

	mustMatch(t, *cluster.applies[0].override, DisplayOverride{Power: displayPowerOff})
}

// The on desire applies a spec with no override, and the API
// server then removes the block this operator owns. The
// display-operator restores the panel from there.
func TestTheOnDesireLiftsTheOverride(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	statePanel(media, []Player{*housePlayer()}, panelDesireOff, nil)
	statePanel(media, []Player{*cluster.players["theater"]}, panelDesireOn, nil)

	mustMatch(t, len(cluster.applies), 2)
	if cluster.applies[1].override != nil {
		t.Errorf("the second apply carried %+v, want no override", cluster.applies[1].override)
	}
	if cluster.displays[testMonitor].Spec.Override != nil {
		t.Errorf("the Display still holds %+v", cluster.displays[testMonitor].Spec.Override)
	}
}

// The retained topic redelivers the same desire on every
// broker session, so the pass writes the Display only when the desire
// changed.
func TestThePassWritesTheDisplayOnlyWhenTheDesireChanges(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	statePanel(media, []Player{*housePlayer()}, panelDesireOff, nil)
	statePanel(media, []Player{*cluster.players["theater"]}, panelDesireOff, nil)

	mustMatch(t, len(cluster.applies), 1)
}

// A screen with no Display keeps its panel lit. There is no
// second writer to fall back to, so the pass writes nothing and the
// Player carries no panel word.
func TestAScreenWithNoDisplayKeepsThePanelLit(t *testing.T) {
	cluster := screenCluster()
	delete(cluster.displays, testMonitor)
	media := testOperator(t, cluster, make(chan struct{}, 1))

	statePanel(media, []Player{*housePlayer()}, panelDesireOff, nil)

	mustMatch(t, len(cluster.applies), 0)
	mustMatch(t, cluster.players["theater"].Status.Panel, "")
}

// A claim the scheduler has not allocated names no screen, so
// the pass writes no override for the unit.
func TestAnUnallocatedClaimNamesNoScreen(t *testing.T) {
	cluster := screenCluster()
	cluster.claims[idleClaimName("theater")].Status = nil
	media := testOperator(t, cluster, make(chan struct{}, 1))

	statePanel(media, []Player{*housePlayer()}, panelDesireOff, nil)

	mustMatch(t, len(cluster.applies), 0)
}

// The Player's panel word is the Display's observation, so a
// person reads what the hardware last showed and not what the media
// layer asked for.
func TestThePlayerStatusFoldsTheDisplayObservation(t *testing.T) {
	cluster := screenCluster()
	cluster.displays[testMonitor].Status.Observed = DisplayObserved{Brightness: ptr(0), Power: "on"}
	media := testOperator(t, cluster, make(chan struct{}, 1))

	statePanel(media, []Player{*housePlayer()}, panelDesireOff, nil)

	mustMatch(t, cluster.players["theater"].Status.Panel, panelBacklightOff)
}

// A unit whose sidecar stated no desire reads no Display at
// all, so a cluster that never darkens a panel costs no request a
// pass.
func TestAUnitWithNoDesireReadsNoDisplay(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.reconcilePlayers([]Player{*housePlayer()}, nil, "", nil)

	mustMatch(t, cluster.players["theater"].Status.Panel, "")
	for _, request := range cluster.requests {
		if request == "GET "+displaysPath+"/"+testMonitor {
			t.Errorf("the pass read the Display: %v", cluster.requests)
		}
	}
}

// An apply the API server refuses leaves the panel dark, so
// the fault reports whichever desire it was. Only a lit panel is
// silent, and a lift that did not land is not one.
func TestAFailedLiftReportsTheFault(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	key := playerKey("house", "theater")

	statePanel(media, []Player{*housePlayer()}, panelDesireOff, nil)
	cluster.applyFails = true
	statePanel(media, []Player{*cluster.players["theater"]}, panelDesireOn, nil)

	if media.panelFaults[key] == "" {
		t.Error("a refused lift reported no fault")
	}
	mustMatch(t, media.panelOverrides[key].desire, panelDesireOff)
}

// A screen the pass cannot read reports the fault for either
// desire, because a Display that answers nothing leaves an override
// standing as readily as it refuses a new one.
func TestAnUnreadableDisplayReportsTheFaultOnEitherDesire(t *testing.T) {
	cases := []string{panelDesireOff, panelDesireOn}
	for _, desire := range cases {
		t.Run(desire, func(t *testing.T) {
			cluster := screenCluster()
			delete(cluster.displays, testMonitor)
			media := testOperator(t, cluster, make(chan struct{}, 1))

			statePanel(media, []Player{*housePlayer()}, desire, nil)

			if media.panelFaults[playerKey("house", "theater")] == "" {
				t.Errorf("the %s desire reported no fault", desire)
			}
		})
	}
}

// A unit with no allocated screen and the on desire is the one
// silent fault. The panel is lit, which is what the desire asks for.
func TestAnUnallocatedScreenIsSilentOnTheOnDesire(t *testing.T) {
	cluster := screenCluster()
	cluster.claims[idleClaimName("theater")].Status = nil
	media := testOperator(t, cluster, make(chan struct{}, 1))

	statePanel(media, []Player{*housePlayer()}, panelDesireOn, nil)

	mustMatch(t, media.panelFaults[playerKey("house", "theater")], "")
}

// A Player deleted while its panel is dark still owes the
// screen a lift. The idle claim goes with the Player, so the pass
// writes the Display from the monitor it remembered when it applied
// the override.
func TestAPrunedUnitLiftsItsOverride(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	statePanel(media, []Player{*housePlayer()}, panelDesireOff, nil)
	delete(cluster.players, "theater")
	media.reconcilePlayers(nil, nil, "", nil)

	mustMatch(t, len(cluster.applies), 2)
	mustMatch(t, cluster.applies[1].name, testMonitor)
	if cluster.applies[1].override != nil {
		t.Errorf("the lift carried %+v, want no override", cluster.applies[1].override)
	}
	if cluster.displays[testMonitor].Spec.Override != nil {
		t.Errorf("the Display still holds %+v", cluster.displays[testMonitor].Spec.Override)
	}
	mustMatch(t, len(media.panelOverrides), 0)
}

// A unit switched to media.liken.sh/none draws nothing, so a dark
// panel it left behind is lifted the way a deleted unit's is, and the
// desk drops the desire so no later pass applies it again.
func TestAUnitSwitchedToNoneLiftsItsOverride(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	key := playerKey("house", "theater")

	statePanel(media, []Player{*housePlayer()}, panelDesireOff, nil)
	player := *cluster.players["theater"]
	player.Spec.Idle = &IdlePolicy{Controller: idleControllerNone}
	media.reconcilePlayers([]Player{player}, nil, "", nil)

	mustMatch(t, len(cluster.applies), 2)
	if cluster.applies[1].override != nil {
		t.Errorf("the lift carried %+v, want no override", cluster.applies[1].override)
	}
	mustMatch(t, len(media.panelOverrides), 0)
	mustMatch(t, media.panels.stateFor(key), "")
	mustMatch(t, cluster.players["theater"].Status.Panel, "")
}

// A unit whose panel was lit owes the screen nothing, so its
// entry goes with no write at all.
func TestAPrunedUnitWithTheOnDesireLiftsNothing(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	statePanel(media, []Player{*housePlayer()}, panelDesireOn, nil)
	applied := len(cluster.applies)
	delete(cluster.players, "theater")
	media.reconcilePlayers(nil, nil, "", nil)

	mustMatch(t, len(cluster.applies), applied)
	mustMatch(t, len(media.panelOverrides), 0)
}

// A lift the API server refuses keeps the entry, so the next
// pass writes the Display again. The panel is dark until one lands.
func TestAFailedPruneLiftRetriesOnTheNextPass(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	key := playerKey("house", "theater")

	statePanel(media, []Player{*housePlayer()}, panelDesireOff, nil)
	delete(cluster.players, "theater")
	cluster.applyFails = true
	media.reconcilePlayers(nil, nil, "", nil)

	mustMatch(t, media.panelOverrides[key].desire, panelDesireOff)
	mustMatch(t, media.panelOverrides[key].monitor, testMonitor)
	if media.panelFaults[key] == "" {
		t.Error("a refused lift reported no fault")
	}

	cluster.applyFails = false
	media.reconcilePlayers(nil, nil, "", nil)

	mustMatch(t, len(media.panelOverrides), 0)
	mustMatch(t, media.panelFaults[key], "")
	if cluster.displays[testMonitor].Spec.Override != nil {
		t.Errorf("the Display still holds %+v", cluster.displays[testMonitor].Spec.Override)
	}
}

// A Display that no longer exists carries no override, so the
// lift it refuses is the lift landing. The entry goes, and nothing
// new is reported.
func TestAPrunedUnitWhoseDisplayIsGoneDropsItsEntry(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	key := playerKey("house", "theater")

	statePanel(media, []Player{*housePlayer()}, panelDesireOff, nil)
	delete(cluster.displays, testMonitor)
	delete(cluster.players, "theater")
	media.reconcilePlayers(nil, nil, "", nil)

	mustMatch(t, len(media.panelOverrides), 0)
	mustMatch(t, media.panelFaults[key], "")
}

// The lookup walks the allocation to the slice entry and
// answers the monitor id the Display is named by.
func TestTheScreenIsFoundThroughTheAllocation(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	monitor, found := newScreens(media.client).monitorFor(housePlayer())

	mustMatch(t, found, true)
	mustMatch(t, monitor, testMonitor)
}

// A device that carries no monitor id names no Display, which
// is a driver that publishes a screen this operator cannot place.
func TestADeviceWithNoMonitorIDNamesNoScreen(t *testing.T) {
	cluster := screenCluster()
	cluster.slices[0].Spec.Devices[0].Attributes = nil
	media := testOperator(t, cluster, make(chan struct{}, 1))

	_, found := newScreens(media.client).monitorFor(housePlayer())

	mustMatch(t, found, false)
}

// The allocated draw device the lookup walks to, named the way a
// ResourceSlice entry is keyed: by driver, by pool, and by device.
func allocatedDrawDevice() DeviceRequestAllocationResult {
	return DeviceRequestAllocationResult{
		Request: idleDrawRequest,
		Driver:  "display.liken.sh",
		Pool:    "nuc5",
		Device:  "card0-dp-1-draw",
	}
}

// The lookup answers no screen when no slice carries the allocated
// device, which is a driver whose slices this operator cannot place.
func TestTheMonitorLookupWalksPastSlicesThatDoNotHoldTheDevice(t *testing.T) {
	otherDriver := monitorSlice()
	otherDriver.Spec.Driver = "gpu.liken.sh"
	otherPool := monitorSlice()
	otherPool.Spec.Pool.Name = "nuc6"
	otherDevice := monitorSlice()
	otherDevice.Spec.Devices[0].Name = "card0-hdmi-1-draw"

	cases := []struct {
		name   string
		slices []ResourceSlice
	}{
		{name: "no driver published a slice"},
		{name: "the slice is another driver's", slices: []ResourceSlice{otherDriver}},
		{name: "the slice is another pool's", slices: []ResourceSlice{otherPool}},
		{name: "the pool holds another device", slices: []ResourceSlice{otherDevice}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			// The slices are already read, so the lookup needs no
			// client and reads the list the case names.
			lookup := &screens{slices: each.slices, listed: true}

			_, found := lookup.monitorOf(allocatedDrawDevice())

			mustMatch(t, found, false)
		})
	}
}

// A slice list the API server refuses names no screen, so the pass
// writes no override rather than one built on a guess.
func TestTheMonitorLookupAnswersNothingWhenTheSliceListFails(t *testing.T) {
	client := testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	_, found := newScreens(client).monitorOf(allocatedDrawDevice())

	mustMatch(t, found, false)
}
