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

// The machine the display driver publishes that screen from, which is
// the machine a Receiver input names.
const testNode = "nuc5"

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
			Driver:   "display.liken.sh",
			Pool:     ResourceSlicePool{Name: "nuc5"},
			NodeName: testNode,
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
		Status: DisplayStatus{
			Observed:   DisplayObserved{Brightness: ptr(70), Power: "on"},
			Conditions: []DisplayCondition{connectedCondition(conditionTrue, "Present", "panel on DP-1")},
		},
	}
}

// The panel as the display-operator reports it while the monitor
// shows another input: the connector carries no panel, and the idle
// claim has deallocated.
func awayDisplay() *Display {
	display := litDisplay()
	display.Status.Conditions = []DisplayCondition{connectedCondition("False", displayReasonNoPanel, "no panel on DP-1")}
	return display
}

func connectedCondition(status, reason, message string) DisplayCondition {
	return DisplayCondition{Type: displayConnectedCondition, Status: status, Reason: reason, Message: message}
}

// The screen the theater unit remembers from an earlier pass, as the
// operator wrote it onto the Player's status.
func rememberedScreen() *PlayerScreenStatus {
	return &PlayerScreenStatus{Node: testNode, Monitor: testMonitor}
}

// The Screen condition the theater unit held from an earlier pass,
// stamped at a time no pass under test writes.
func heldScreenCondition(status, reason string) PlayerCondition {
	return PlayerCondition{
		Type:               screenConditionType,
		Status:             status,
		Reason:             reason,
		LastTransitionTime: "2020-01-01T00:00:00Z",
	}
}

// screenCondition answers the one Screen condition the written
// Player carries, and fails the test when it carries none or more.
func screenCondition(t *testing.T, player *Player) PlayerCondition {
	t.Helper()
	if len(player.Status.Conditions) != 1 {
		t.Fatalf("the Player carries %+v, want one Screen condition", player.Status.Conditions)
	}
	condition := player.Status.Conditions[0]
	mustMatch(t, condition.Type, screenConditionType)
	return condition
}

// displayReads counts the passes' reads of the screen's Display.
func displayReads(cluster *fakeCluster) int {
	reads := 0
	for _, request := range cluster.requests {
		if request == "GET "+displaysPath+"/"+testMonitor {
			reads++
		}
	}
	return reads
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
	mustMatch(t, cluster.applies[0].manager, applyFieldManager)
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

// A unit with no screen memory reads no Display at all: its claim
// has never resolved, so there is no Display to read a condition
// from, and it carries no Screen condition. A unit with a screen
// reads its Display once a pass whether or not a desire stands,
// because the condition is what says why the idle pod waits.
func TestAUnitWithNoScreenMemoryReadsNoDisplay(t *testing.T) {
	cluster := screenCluster()
	cluster.claims[idleClaimName("theater")].Status = nil
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.reconcilePlayers([]Player{*housePlayer()}, nil, "", nil)

	mustMatch(t, cluster.players["theater"].Status.Panel, "")
	mustMatch(t, len(cluster.players["theater"].Status.Conditions), 0)
	if cluster.players["theater"].Status.Screen != nil {
		t.Errorf("the Player remembers %+v, want no screen", cluster.players["theater"].Status.Screen)
	}
	mustMatch(t, displayReads(cluster), 0)
}

// The pass writes the screen the idle claim resolved to onto the
// Player, so a person reads which machine and which Display the unit
// draws on.
func TestThePassRemembersTheScreenFromAnAllocatedClaim(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.reconcilePlayers([]Player{*housePlayer()}, nil, "", nil)

	mustMatch(t, *cluster.players["theater"].Status.Screen, *rememberedScreen())
}

// A claim that deallocated names no screen, so the pass keeps the
// screen the Player remembers and reads that Display for the
// condition. The monitor showing another input is the PanelAway
// reason, with the Display's own message.
func TestThePassKeepsTheRememberedScreenWhenTheClaimIsUnallocated(t *testing.T) {
	cluster := screenCluster()
	cluster.claims[idleClaimName("theater")].Status = nil
	cluster.displays[testMonitor] = awayDisplay()
	player := housePlayer()
	player.Status.Screen = rememberedScreen()
	cluster.players["theater"] = player
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.reconcilePlayers([]Player{*player}, nil, "", nil)

	mustMatch(t, *cluster.players["theater"].Status.Screen, *rememberedScreen())
	condition := screenCondition(t, cluster.players["theater"])
	mustMatch(t, condition.Status, "False")
	mustMatch(t, condition.Reason, screenReasonPanelAway)
	mustMatch(t, condition.Message, "no panel on DP-1")
}

// The Screen condition is the Display's Connected condition read
// for the unit: present, away, or unknown when there is no Display
// or no report to read.
func TestTheScreenConditionFollowsTheDisplay(t *testing.T) {
	unreported := litDisplay()
	unreported.Status.Conditions = nil
	sleeping := litDisplay()
	sleeping.Status.Conditions = []DisplayCondition{connectedCondition("False", "Sleeping", "panel asleep")}

	cases := []struct {
		name    string
		display *Display
		want    PlayerCondition
	}{
		{
			name:    "a lit panel is present",
			display: litDisplay(),
			want:    PlayerCondition{Status: conditionTrue, Reason: screenReasonPresent, Message: "panel on DP-1"},
		},
		{
			name:    "a connector with no panel is away",
			display: awayDisplay(),
			want:    PlayerCondition{Status: "False", Reason: screenReasonPanelAway, Message: "no panel on DP-1"},
		},
		{
			name:    "any other reason passes through",
			display: sleeping,
			want:    PlayerCondition{Status: "False", Reason: "Sleeping", Message: "panel asleep"},
		},
		{
			name: "a missing Display is unknown",
			want: PlayerCondition{Status: "Unknown", Reason: screenReasonNoDisplay, Message: "no Display named DP-1"},
		},
		{
			name:    "a Display with no Connected condition is unreported",
			display: unreported,
			want:    PlayerCondition{Status: "Unknown", Reason: screenReasonNotReported},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := screenCluster()
			delete(cluster.displays, testMonitor)
			if each.display != nil {
				cluster.displays[testMonitor] = each.display
			}
			media := testOperator(t, cluster, make(chan struct{}, 1))

			media.reconcilePlayers([]Player{*housePlayer()}, nil, "", nil)

			condition := screenCondition(t, cluster.players["theater"])
			mustMatch(t, condition.Status, each.want.Status)
			mustMatch(t, condition.Reason, each.want.Reason)
			mustMatch(t, condition.Message, each.want.Message)
			if condition.LastTransitionTime == "" {
				t.Error("the condition carries no transition time")
			}
		})
	}
}

// The transition time is the moment the status last changed, so a
// pass that reads the same status keeps the time the Player already
// holds, and a pass that reads a different one stamps a new time.
func TestTheTransitionTimeMovesOnlyWhenTheStatusFlips(t *testing.T) {
	cases := []struct {
		name    string
		display *Display
		moves   bool
	}{
		{name: "the same status keeps the time", display: litDisplay(), moves: false},
		{name: "a flipped status stamps a new time", display: awayDisplay(), moves: true},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := screenCluster()
			cluster.displays[testMonitor] = each.display
			player := housePlayer()
			player.Status.Screen = rememberedScreen()
			player.Status.Conditions = []PlayerCondition{heldScreenCondition(conditionTrue, screenReasonPresent)}
			cluster.players["theater"] = player
			media := testOperator(t, cluster, make(chan struct{}, 1))

			media.reconcilePlayers([]Player{*player}, nil, "", nil)

			condition := screenCondition(t, cluster.players["theater"])
			mustMatch(t, condition.LastTransitionTime != "2020-01-01T00:00:00Z", each.moves)
		})
	}
}

// A Display the pass cannot read is not a change on the panel, so
// the unit keeps the condition it already holds rather than churning
// on a transient error, and the fault reports once.
func TestAnUnreadableDisplayKeepsTheHeldCondition(t *testing.T) {
	cluster := screenCluster()
	cluster.fails[displaysPath+"/"+testMonitor] = true
	player := housePlayer()
	player.Status.Screen = rememberedScreen()
	player.Status.Conditions = []PlayerCondition{heldScreenCondition("False", screenReasonPanelAway)}
	cluster.players["theater"] = player
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.reconcilePlayers([]Player{*player}, nil, "", nil)
	media.reconcilePlayers([]Player{*cluster.players["theater"]}, nil, "", nil)

	mustMatch(t, screenCondition(t, cluster.players["theater"]), heldScreenCondition("False", screenReasonPanelAway))
	if media.panelFaults[playerKey("house", "theater")] == "" {
		t.Error("an unreadable Display reported no fault")
	}
}

// The condition and the panel both read the same Display, so a unit
// costs one read a pass however many questions the pass asks of it.
func TestAUnitReadsItsDisplayOnceAPass(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	statePanel(media, []Player{*housePlayer()}, panelDesireOff, nil)

	mustMatch(t, displayReads(cluster), 1)
	mustMatch(t, cluster.players["theater"].Status.Panel, panelOn)
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

	found, resolved := newScreens(media.client).screenFor(housePlayer())

	mustMatch(t, resolved, true)
	mustMatch(t, found.monitor, testMonitor)
	mustMatch(t, found.node, testNode)
}

// A device that carries no monitor id names no Display, which
// is a driver that publishes a screen this operator cannot place.
func TestADeviceWithNoMonitorIDNamesNoScreen(t *testing.T) {
	cluster := screenCluster()
	cluster.slices[0].Spec.Devices[0].Attributes = nil
	media := testOperator(t, cluster, make(chan struct{}, 1))

	_, found := newScreens(media.client).screenFor(housePlayer())

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

			_, found := lookup.screenOf(allocatedDrawDevice())

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

	_, found := newScreens(client).screenOf(allocatedDrawDevice())

	mustMatch(t, found, false)
}
