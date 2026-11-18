package main

// These tests cover how media-operator uses Display resources: finding
// the Display from an allocated draw device, the override that each
// desire applies to it, and what happens when there is no Display.

import (
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// testMonitor is the monitor id that display-operator publishes for the
// screen. It is also the name of the Display.
const testMonitor = "DP-1"

// testNode is the machine whose display driver publishes the screen. A
// Receiver input refers to this machine.
const testNode = "node-5"

// allocatedIdleClaim returns the idle claim after the scheduler
// allocated its draw request to one device in the display driver's
// pool.
func allocatedIdleClaim() *ResourceClaim {
	claim := buildIdleClaim(housePlayer(), "display-draw")
	claim.Status = &ResourceClaimStatus{
		Allocation: &DeviceAllocationResult{
			Devices: DeviceAllocationDevices{
				Results: []DeviceRequestAllocationResult{{
					Request: idleDrawRequest,
					Driver:  "display.liken.sh",
					Pool:    "node-5",
					Device:  "card0-dp-1-draw",
				}},
			},
		},
	}
	return claim
}

// monitorSlice returns the ResourceSlice with the allocated device and
// its attributes, including the monitor id.
func monitorSlice() ResourceSlice {
	return ResourceSlice{
		Metadata: ObjectMeta{Name: "node-5-display"},
		Spec: ResourceSliceSpec{
			Driver:   "display.liken.sh",
			Pool:     ResourceSlicePool{Name: "node-5"},
			NodeName: testNode,
			Devices: []ResourceSliceItem{{
				Name:       "card0-dp-1-draw",
				Attributes: map[string]DeviceAttribute{monitorIDAttribute: {String: ptr(testMonitor)}},
			}},
		},
	}
}

// litDisplay returns a Display for a lit panel with no override.
func litDisplay() *Display {
	return &Display{
		Metadata: ObjectMeta{Name: testMonitor},
		Status: DisplayStatus{
			Observed:   DisplayObserved{Brightness: ptr(70), Power: "on"},
			Conditions: []DisplayCondition{connectedCondition(conditionTrue, "Present", "panel on DP-1")},
		},
	}
}

// awayDisplay returns a Display whose connector has no panel, as when
// the monitor shows another input.
func awayDisplay() *Display {
	display := litDisplay()
	display.Status.Conditions = []DisplayCondition{connectedCondition("False", displayReasonNoPanel, "no panel on DP-1")}
	return display
}

func connectedCondition(status, reason, message string) DisplayCondition {
	return DisplayCondition{Type: displayConnectedCondition, Status: status, Reason: reason, Message: message}
}

// earlierScreen is the screen that an earlier pass wrote to the
// theater Player's status.
func earlierScreen() *PlayerScreenStatus {
	return &PlayerScreenStatus{Node: testNode, Monitor: testMonitor}
}

// earlierScreenCondition is a Screen condition that an earlier pass
// wrote, with a transition time that no pass in the tests writes.
func earlierScreenCondition(status conditions.Status, reason string) PlayerCondition {
	return PlayerCondition{
		Type:               screenConditionType,
		Status:             status,
		Reason:             reason,
		LastTransitionTime: earlierTransitionTime,
	}
}

var earlierTransitionTime = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// screenCondition returns the Screen condition of the written Player.
// It fails the test unless the Player has exactly one condition.
func screenCondition(t *testing.T, player *Player) PlayerCondition {
	t.Helper()
	if len(player.Status.Conditions) != 1 {
		t.Fatalf("the Player conditions are %+v, want one Screen condition", player.Status.Conditions)
	}
	condition := player.Status.Conditions[0]
	mustMatch(t, condition.Type, screenConditionType)
	return condition
}

func displayReads(cluster *fakeCluster) int {
	reads := 0
	for _, request := range cluster.requests {
		if request == "GET "+displaysPath+"/"+testMonitor {
			reads++
		}
	}
	return reads
}

// screenCluster returns a cluster where the theater Player has an
// allocated screen and a Display.
func screenCluster() *fakeCluster {
	cluster := newFakeCluster()
	cluster.players["theater"] = housePlayer()
	cluster.claims[idleClaimName("theater")] = allocatedIdleClaim()
	cluster.slices = []ResourceSlice{monitorSlice()}
	cluster.displays[testMonitor] = litDisplay()
	return cluster
}

// sendPanelDesireAndReconcile sends one panel desire as a bus message and reconciles
// the Players. It clears the cached lookups first, as pass does, so the
// reconcile reads the Display as the previous one left it.
func sendPanelDesireAndReconcile(media *operator, players []Player, desire string, defaults *IdlePolicy) {
	media.screenCache, media.receiverCache = nil, nil
	media.handleBusMessage(playerPanelTopic(defaultTopicBase, "house", "theater"),
		[]byte(`{"desire":"`+desire+`"}`))
	media.reconcilePlayers(players, nil, "", defaults)
}

// The off desire applies a backlight override to the screen's Display
// under this operator's field manager.
func TestTheOffDesireOverridesTheBacklight(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, nil)

	mustMatch(t, len(cluster.applies), 1)
	mustMatch(t, cluster.applies[0].name, testMonitor)
	mustMatch(t, cluster.applies[0].manager, applyFieldManager)
	mustMatch(t, *cluster.applies[0].override, DisplayOverride{Backlight: displayPowerOff})
	mustMatch(t, *cluster.displays[testMonitor].Spec.Override, DisplayOverride{Backlight: displayPowerOff})
}

func TestTheOffDesireOverridesThePowerWhenTheOffModeIsPower(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, &IdlePolicy{OffMode: offModePower})

	mustMatch(t, *cluster.applies[0].override, DisplayOverride{Power: displayPowerOff})
}

// The on desire applies a spec with no override, so the API server
// removes the override that this field manager owns. display-operator
// then restores the panel's settings.
func TestTheOnDesireRemovesTheOverride(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, nil)
	sendPanelDesireAndReconcile(media, []Player{*cluster.players["theater"]}, panelDesireOn, nil)

	mustMatch(t, len(cluster.applies), 2)
	if cluster.applies[1].override != nil {
		t.Errorf("the second apply wrote override %+v, want none", cluster.applies[1].override)
	}
	if cluster.displays[testMonitor].Spec.Override != nil {
		t.Errorf("the Display override is %+v, want none", cluster.displays[testMonitor].Spec.Override)
	}
}

// The broker redelivers the retained desire on every new session, so
// the pass writes the Display only when the desire has changed.
func TestThePassWritesTheDisplayOnlyWhenTheDesireChanges(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, nil)
	sendPanelDesireAndReconcile(media, []Player{*cluster.players["theater"]}, panelDesireOff, nil)

	mustMatch(t, len(cluster.applies), 1)
}

func TestAScreenWithNoDisplayKeepsThePanelLit(t *testing.T) {
	cluster := screenCluster()
	delete(cluster.displays, testMonitor)
	media := testOperator(t, cluster, make(chan struct{}, 1))

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, nil)

	mustMatch(t, len(cluster.applies), 0)
	mustMatch(t, cluster.players["theater"].Status.Panel, "")
}

func TestAnUnallocatedClaimWritesNoOverride(t *testing.T) {
	cluster := screenCluster()
	cluster.claims[idleClaimName("theater")].Status = nil
	media := testOperator(t, cluster, make(chan struct{}, 1))

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, nil)

	mustMatch(t, len(cluster.applies), 0)
}

// The Player's panel status shows what the panel last reported, not
// the override that media-operator applied.
func TestThePlayerPanelStatusComesFromTheObservedState(t *testing.T) {
	cluster := screenCluster()
	cluster.displays[testMonitor].Status.Observed = DisplayObserved{Brightness: ptr(0), Power: "on"}
	media := testOperator(t, cluster, make(chan struct{}, 1))

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, nil)

	mustMatch(t, cluster.players["theater"].Status.Panel, panelBacklightOff)
}

// A Player whose claim is not allocated and whose status has no screen
// has no Display to read, so it gets no Screen condition.
func TestAPlayerWithNoKnownScreenReadsNoDisplay(t *testing.T) {
	cluster := screenCluster()
	cluster.claims[idleClaimName("theater")].Status = nil
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.reconcilePlayers([]Player{*housePlayer()}, nil, "", nil)

	mustMatch(t, cluster.players["theater"].Status.Panel, "")
	mustMatch(t, len(cluster.players["theater"].Status.Conditions), 0)
	if cluster.players["theater"].Status.Screen != nil {
		t.Errorf("the Player status.screen is %+v, want none", cluster.players["theater"].Status.Screen)
	}
	mustMatch(t, displayReads(cluster), 0)
}

func TestThePassWritesTheScreenFromAnAllocatedClaimToTheStatus(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.reconcilePlayers([]Player{*housePlayer()}, nil, "", nil)

	mustMatch(t, *cluster.players["theater"].Status.Screen, *earlierScreen())
}

// When the claim is no longer allocated, the pass keeps the screen in
// the Player's status and reads that Display for the condition.
func TestThePassKeepsTheStatusScreenWhenTheClaimIsUnallocated(t *testing.T) {
	cluster := screenCluster()
	cluster.claims[idleClaimName("theater")].Status = nil
	cluster.displays[testMonitor] = awayDisplay()
	player := housePlayer()
	player.Status.Screen = earlierScreen()
	cluster.players["theater"] = player
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.reconcilePlayers([]Player{*player}, nil, "", nil)

	mustMatch(t, *cluster.players["theater"].Status.Screen, *earlierScreen())
	condition := screenCondition(t, cluster.players["theater"])
	mustMatch(t, condition.Status, "False")
	mustMatch(t, condition.Reason, screenReasonPanelAway)
	mustMatch(t, condition.Message, "no panel on DP-1")
}

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
			if condition.LastTransitionTime.IsZero() {
				t.Error("the Screen condition has no lastTransitionTime, want one")
			}
		})
	}
}

func TestTheTransitionTimeChangesOnlyWhenTheStatusChanges(t *testing.T) {
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
			player.Status.Screen = earlierScreen()
			player.Status.Conditions = []PlayerCondition{earlierScreenCondition(conditionTrue, screenReasonPresent)}
			cluster.players["theater"] = player
			media := testOperator(t, cluster, make(chan struct{}, 1))

			media.reconcilePlayers([]Player{*player}, nil, "", nil)

			condition := screenCondition(t, cluster.players["theater"])
			mustMatch(t, !condition.LastTransitionTime.Equal(earlierTransitionTime), each.moves)
		})
	}
}

// A read error says nothing about the panel, so the Player keeps its
// current condition, and the fault is reported once.
func TestAnUnreadableDisplayKeepsTheCurrentCondition(t *testing.T) {
	cluster := screenCluster()
	cluster.fails[displaysPath+"/"+testMonitor] = true
	player := housePlayer()
	player.Status.Screen = earlierScreen()
	player.Status.Conditions = []PlayerCondition{earlierScreenCondition("False", screenReasonPanelAway)}
	cluster.players["theater"] = player
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.reconcilePlayers([]Player{*player}, nil, "", nil)
	media.reconcilePlayers([]Player{*cluster.players["theater"]}, nil, "", nil)

	mustMatch(t, screenCondition(t, cluster.players["theater"]), earlierScreenCondition("False", screenReasonPanelAway))
	if media.panelFaults[playerKey("house", "theater")] == "" {
		t.Error("after the Display could not be read, panelFaults has no entry, want one")
	}
}

// The Screen condition and the panel read the Display from the cluster
// view's watch cache, so the pass sends the API server no request.
func TestAPlayerReadsItsDisplayFromTheView(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, nil)

	mustMatch(t, displayReads(cluster), 0)
	mustMatch(t, cluster.players["theater"].Status.Panel, panelOn)
}

// A rejected removal of the override leaves the panel off, so it
// reports a fault, and the recorded off override stays for a retry.
func TestARejectedOverrideRemovalReportsTheFault(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	key := playerKey("house", "theater")

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, nil)
	cluster.applyFails = true
	sendPanelDesireAndReconcile(media, []Player{*cluster.players["theater"]}, panelDesireOn, nil)

	if media.panelFaults[key] == "" {
		t.Error("after the Display refused the lift, panelFaults has no entry, want one")
	}
	mustMatch(t, media.panelOverrides[key].desire, panelDesireOff)
}

func TestAMissingDisplayReportsTheFaultOnEitherDesire(t *testing.T) {
	cases := []string{panelDesireOff, panelDesireOn}
	for _, desire := range cases {
		t.Run(desire, func(t *testing.T) {
			cluster := screenCluster()
			delete(cluster.displays, testMonitor)
			media := testOperator(t, cluster, make(chan struct{}, 1))

			sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, desire, nil)

			if media.panelFaults[playerKey("house", "theater")] == "" {
				t.Errorf("with the %s desire and no Display, panelFaults has no entry, want one", desire)
			}
		})
	}
}

func TestAnUnallocatedScreenIsSilentOnTheOnDesire(t *testing.T) {
	cluster := screenCluster()
	cluster.claims[idleClaimName("theater")].Status = nil
	media := testOperator(t, cluster, make(chan struct{}, 1))

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOn, nil)

	mustMatch(t, media.panelFaults[playerKey("house", "theater")], "")
}

// The idle claim is deleted with the Player, so the pass removes the
// override from the Display whose monitor id it stored when it applied
// the override.
func TestADeletedPlayerHasItsOverrideRemoved(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, nil)
	delete(cluster.players, "theater")
	media.reconcilePlayers(nil, nil, "", nil)

	mustMatch(t, len(cluster.applies), 2)
	mustMatch(t, cluster.applies[1].name, testMonitor)
	if cluster.applies[1].override != nil {
		t.Errorf("the lift wrote override %+v, want none", cluster.applies[1].override)
	}
	if cluster.displays[testMonitor].Spec.Override != nil {
		t.Errorf("the Display override is %+v, want none", cluster.displays[testMonitor].Spec.Override)
	}
	mustMatch(t, len(media.panelOverrides), 0)
}

// A Player switched to media.liken.sh/none draws nothing, so its
// override is removed as for a deleted Player, and its stored desire is
// dropped so no later pass applies it again.
func TestAPlayerSwitchedToNoneHasItsOverrideRemoved(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	key := playerKey("house", "theater")

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, nil)
	player := *cluster.players["theater"]
	player.Spec.Idle = &IdlePolicy{Controller: idleControllerNone}
	media.reconcilePlayers([]Player{player}, nil, "", nil)

	mustMatch(t, len(cluster.applies), 2)
	if cluster.applies[1].override != nil {
		t.Errorf("the lift wrote override %+v, want none", cluster.applies[1].override)
	}
	mustMatch(t, len(media.panelOverrides), 0)
	mustMatch(t, media.panels.stateFor(key), "")
	mustMatch(t, cluster.players["theater"].Status.Panel, "")
}

func TestADeletedPlayerWithALitPanelWritesNothing(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOn, nil)
	applied := len(cluster.applies)
	delete(cluster.players, "theater")
	media.reconcilePlayers(nil, nil, "", nil)

	mustMatch(t, len(cluster.applies), applied)
	mustMatch(t, len(media.panelOverrides), 0)
}

func TestARejectedRemovalForADeletedPlayerRetriesOnTheNextPass(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	key := playerKey("house", "theater")

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, nil)
	delete(cluster.players, "theater")
	cluster.applyFails = true
	media.reconcilePlayers(nil, nil, "", nil)

	mustMatch(t, media.panelOverrides[key].desire, panelDesireOff)
	mustMatch(t, media.panelOverrides[key].monitor, testMonitor)
	if media.panelFaults[key] == "" {
		t.Error("after the Display refused the lift, panelFaults has no entry, want one")
	}

	cluster.applyFails = false
	media.reconcilePlayers(nil, nil, "", nil)

	mustMatch(t, len(media.panelOverrides), 0)
	mustMatch(t, media.panelFaults[key], "")
	if cluster.displays[testMonitor].Spec.Override != nil {
		t.Errorf("the Display override is %+v, want none", cluster.displays[testMonitor].Spec.Override)
	}
}

// A Display that no longer exists has no override, so the NotFound
// error drops the entry with no fault.
func TestADeletedPlayerWhoseDisplayIsGoneDropsItsEntry(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	key := playerKey("house", "theater")

	sendPanelDesireAndReconcile(media, []Player{*housePlayer()}, panelDesireOff, nil)
	delete(cluster.displays, testMonitor)
	delete(cluster.players, "theater")
	media.reconcilePlayers(nil, nil, "", nil)

	mustMatch(t, len(media.panelOverrides), 0)
	mustMatch(t, media.panelFaults[key], "")
}

func TestTheScreenIsFoundThroughTheAllocation(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	found, resolved := newScreens(media.view).screenFor(housePlayer())

	mustMatch(t, resolved, true)
	mustMatch(t, found.monitor, testMonitor)
	mustMatch(t, found.node, testNode)
}

func TestADeviceWithNoMonitorIDNamesNoScreen(t *testing.T) {
	cluster := screenCluster()
	cluster.slices[0].Spec.Devices[0].Attributes = nil
	media := testOperator(t, cluster, make(chan struct{}, 1))

	_, found := newScreens(media.view).screenFor(housePlayer())

	mustMatch(t, found, false)
}

func allocatedDrawDevice() DeviceRequestAllocationResult {
	return DeviceRequestAllocationResult{
		Request: idleDrawRequest,
		Driver:  "display.liken.sh",
		Pool:    "node-5",
		Device:  "card0-dp-1-draw",
	}
}

func TestTheMonitorLookupSkipsSlicesThatDoNotListTheDevice(t *testing.T) {
	otherDriver := monitorSlice()
	otherDriver.Spec.Driver = "gpu.liken.sh"
	otherPool := monitorSlice()
	otherPool.Spec.Pool.Name = "node-6"
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
			// listed is true, so the lookup uses the slices of the case
			// and does not read them from the view.
			lookup := &screens{slices: each.slices, listed: true}

			_, found := lookup.screenOfDevice(allocatedDrawDevice())

			mustMatch(t, found, false)
		})
	}
}

func TestTheMonitorLookupReturnsNoScreenWhenTheSliceReadFails(t *testing.T) {
	cluster := screenCluster()
	cluster.fails[slicesPath] = true

	_, found := newScreens(cluster.view()).screenOfDevice(allocatedDrawDevice())

	mustMatch(t, found, false)
}
