package main

// Tests for the controller. The receiver and the API server are fakes
// on in-memory connections, and time is the fake clock of a synctest
// bubble.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

func TestPokeNeverBlocksAndDrainPokesClearsTheQueue(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wake := make(chan struct{}, 1)

		sendWake(wake)
		sendWake(wake)
		mustMatch(t, len(wake), 1)

		drainWakes(wake)
		mustMatch(t, len(wake), 0)
	})
}

// fakeAPI is a fake API server. It serves the collection that a test
// sets and records every status and power that the operator applies.
type fakeAPI struct {
	client  *Client
	events  *eventstest.Events
	written chan ReceiverStatus
	powers  chan equipment.Power

	mutex        sync.Mutex
	list         ReceiverList
	broken       bool
	statuses     []ReceiverStatus
	statusBodies [][]byte
	powersSet    []equipment.Power
	refusing     bool
	powerGate    chan struct{}
	// lists counts the list requests for Receivers.
	lists int
	// applied lists the name of the Receiver in each apply on the main
	// resource.
	applied []string
	// throttlingStatus makes every status write fail with a 429 that
	// asks for a five-second wait, and statusThrottles counts those
	// responses.
	throttlingStatus bool
	statusThrottles  int
	// eventGate blocks each Event write until releaseEvents.
	eventGate chan struct{}
}

func startFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	api := &fakeAPI{written: make(chan ReceiverStatus, 64), powers: make(chan equipment.Power, 64), events: &eventstest.Events{}}
	answer := api.events.Around(http.HandlerFunc(api.handle))
	api.client = testAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mutex.Lock()
		gate := api.eventGate
		api.mutex.Unlock()
		if gate != nil && strings.Contains(r.URL.Path, "/events") {
			<-gate
		}
		answer.ServeHTTP(w, r)
	}))
	return api
}

// gateEvents blocks every Event write until releaseEvents, so a test
// can keep the recorder's request open.
func (a *fakeAPI) gateEvents() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.eventGate = make(chan struct{})
}

func (a *fakeAPI) releaseEvents() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	close(a.eventGate)
	a.eventGate = nil
}

func (a *fakeAPI) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/status") {
		a.recordStatus(w, r)
		return
	}
	if r.Method == http.MethodPatch {
		a.recordPower(w, r)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == receiversPath {
		if r.URL.Query().Get("watch") == "true" {
			a.serveWatch(w, r)
			return
		}
		a.serveList(w)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (a *fakeAPI) serveList(w http.ResponseWriter) {
	a.mutex.Lock()
	a.lists++
	broken, list := a.broken, a.list
	a.mutex.Unlock()
	if broken {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(list)
}

// serveWatch sends the Receivers that a test set as the watch's initial
// events, as the API server does, and then keeps the connection open
// with no later event, because each test runs its own passes.
func (a *fakeAPI) serveWatch(w http.ResponseWriter, r *http.Request) {
	a.mutex.Lock()
	list := a.list
	a.mutex.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Query().Get("sendInitialEvents") == "true" {
		for _, receiver := range list.Items {
			receiver.APIVersion, receiver.Kind = equipmentAPIVersion, "Receiver"
			encoded, _ := json.Marshal(map[string]any{"type": "ADDED", "object": receiver})
			_, _ = w.Write(append(encoded, '\n'))
		}
		_, _ = io.WriteString(w, initialEventsEnd(equipmentAPIVersion, "Receiver", list.Metadata.ResourceVersion)+"\n")
	}
	w.(http.Flusher).Flush()
	<-r.Context().Done()
}

func (a *fakeAPI) recordStatus(w http.ResponseWriter, r *http.Request) {
	a.mutex.Lock()
	refusing := a.refusing
	throttling := a.throttlingStatus
	if throttling {
		a.statusThrottles++
	}
	a.mutex.Unlock()
	if throttling {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if refusing {
		select {
		case a.written <- ReceiverStatus{}:
		default:
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	body, _ := io.ReadAll(r.Body)
	var applied receiverStatusApply
	_ = json.Unmarshal(body, &applied)
	a.mutex.Lock()
	a.statuses = append(a.statuses, applied.Status)
	a.statusBodies = append(a.statusBodies, body)
	a.mutex.Unlock()
	select {
	case a.written <- applied.Status:
	default:
	}
	_ = json.NewEncoder(w).Encode(&Receiver{Metadata: applied.Metadata, Status: ReceiverStoredStatus{ReceiverStatus: applied.Status}})
}

// recordPower handles the operator's apply on the main resource, which
// owns spec.power, and records the power in the request body.
func (a *fakeAPI) recordPower(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var applied receiverPowerApply
	_ = json.Unmarshal(body, &applied)
	a.mutex.Lock()
	a.applied = append(a.applied, path.Base(r.URL.Path))
	a.powersSet = append(a.powersSet, applied.Spec.Power)
	gate := a.powerGate
	a.mutex.Unlock()
	select {
	case a.powers <- applied.Spec.Power:
	default:
	}
	if gate != nil {
		<-gate
	}
	_ = json.NewEncoder(w).Encode(&Receiver{Metadata: applied.Metadata, Spec: applied.Spec})
}

// appliedReceivers returns the name of the Receiver in each apply on
// the main resource, in order.
func (a *fakeAPI) appliedReceivers() []string {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return append([]string(nil), a.applied...)
}

func (a *fakeAPI) setReceivers(items ...Receiver) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.list = ReceiverList{Metadata: ListMeta{ResourceVersion: "1"}, Items: items}
}

// gatePowers blocks every power write until releasePowers, so a test
// can keep the API request open.
func (a *fakeAPI) gatePowers() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.powerGate = make(chan struct{})
}

func (a *fakeAPI) releasePowers() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.powerGate != nil {
		close(a.powerGate)
		a.powerGate = nil
	}
}

func (a *fakeAPI) breakTheStatus(refusing bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.refusing = refusing
}

func (a *fakeAPI) breakTheList() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.broken = true
}

func (a *fakeAPI) lastStatus() ReceiverStatus {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.statuses[len(a.statuses)-1]
}

func (a *fakeAPI) writeCount() int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return len(a.statuses)
}

// waitForStatus reads the applied statuses until one satisfies ready,
// and returns it.
func (a *fakeAPI) waitForStatus(t *testing.T, ready func(ReceiverStatus) bool) ReceiverStatus {
	t.Helper()
	deadline := time.After(testTimeout)
	for {
		select {
		case status := <-a.written:
			if ready(status) {
				return status
			}
		case <-deadline:
			t.Fatal("no applied status satisfied the test before the deadline, want one")
			return ReceiverStatus{}
		}
	}
}

// refuseStatus fails the test if the operator applies any status within
// the given time.
func (a *fakeAPI) refuseStatus(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case status := <-a.written:
		t.Fatalf("the operator applied the status %+v, want no status write", status)
	case <-time.After(within):
	}
}

func testReceiver(name, address string) Receiver {
	return Receiver{
		Metadata: ObjectMeta{Name: name, Generation: 4},
		Spec:     ReceiverSpec{Denon: &DenonProtocol{Address: address}},
	}
}

// startController does not change statusDebounce, because a unit's
// writer goroutine outlives the test and would race with restoring the
// value.
func startController(t *testing.T, api *fakeAPI) *controller {
	t.Helper()
	operator := newController(api.client, testMetrics(t))
	operator.dial = testNetwork.dial
	operator.listen = testNetwork.bind
	operator.now = func() time.Time { return statusNow }
	operator.recorder = testRecorder(t, api.client)
	return operator
}

// waitForFirstRead waits until the driver of each unit whose receiver is
// reachable has finished its first full read of it, because the operator applies settings only
// after the first full read.
func waitForFirstRead(t *testing.T, operator *controller) {
	t.Helper()
	deadline := time.After(4 * time.Second)
	for {
		waiting := false
		for _, unit := range operator.units {
			if unit.driver == nil {
				continue
			}
			if unit.driver.State().Reachable == equipment.ConditionTrue && !unit.driver.FirstReadDone() {
				waiting = true
			}
		}
		if !waiting {
			return
		}
		select {
		case <-deadline:
			t.Fatal("the driver had not finished its first full read of the receiver before the deadline, want it finished")
		case <-time.After(time.Millisecond):
		}
	}
}

// connected returns true when status has exactly one condition other
// than InputSelected, which a running session adds, and it is True.
func connected(status ReceiverStatus) bool {
	others := slices.DeleteFunc(slices.Clone(status.Conditions), func(condition Condition) bool {
		return condition.Type == inputSelectedConditionType
	})
	return len(others) == 1 && others[0].Status == ConditionTrue
}

func TestTheOperatorReportsWhatTheReceiverSaid(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", equipment.address()))
		operator := startController(t, api)

		mustSucceed(t, operator.pass(t.Context()))

		status := api.waitForStatus(t, func(status ReceiverStatus) bool {
			return connected(status) && status.Zones["main"].SoundMode != ""
		})
		mustMatch(t, status.Zones["main"].Power, "Standby")
		mustMatch(t, status.Zones["main"].Input, "MPLAY")
		mustMatch(t, status.Zones["main"].Volume, "50")
		mustMatch(t, status.Zones["main"].VolumeMax, "69.5")
		mustMatch(t, status.Zones["main"].Mute, false)
		mustMatch(t, status.Zones["main"].SoundMode, "MULTI CH IN")
		mustMatch(t, status.Conditions[0].Reason, reasonConnected)
		mustMatch(t, status.Conditions[0].ObservedGeneration, int64(4))
	})
}

func TestTheConnectBurstMakesOneStatusWrite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", equipment.address()))
		operator := startController(t, api)

		mustSucceed(t, operator.pass(t.Context()))

		api.waitForStatus(t, func(status ReceiverStatus) bool {
			return connected(status) && status.Zones["main"].SoundMode != ""
		})
		mustMatch(t, api.writeCount(), 1)
	})
}

func TestABurstOfKnobTurnsMakesOneStatusWrite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", equipment.address()))
		operator := startController(t, api)
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)

		before := api.writeCount()
		equipment.turnKnob(110)
		equipment.turnKnob(120)
		equipment.turnKnob(130)

		api.waitForStatus(t, func(status ReceiverStatus) bool { return status.Zones["main"].Volume == "65" })
		mustMatch(t, api.writeCount(), before+1)
	})
}

func TestASecondPassWritesNothingFurther(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", equipment.address()))
		operator := startController(t, api)
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		// The first pass after the first full read records the declared blocks
		// as settled, which changes the status. The next pass has
		// nothing left to write.
		waitForFirstRead(t, operator)
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, func(status ReceiverStatus) bool { return len(status.SettledSettings) > 0 })

		mustSucceed(t, operator.pass(t.Context()))

		api.refuseStatus(t, 2*statusDebounce)
	})
}

// A receiver repeats a line when its own menu or remote touches a
// field, often with the value it already reported. The status that the
// burst builds is unchanged, so the operator writes nothing.
func TestALineThatRepeatsTheStateWritesNoStatus(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", equipment.address()))
		operator := startController(t, api)
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForFirstRead(t, operator)
		mustSucceed(t, operator.pass(t.Context()))
		settled := api.waitForStatus(t, func(status ReceiverStatus) bool { return len(status.SettledSettings) > 0 })

		equipment.volunteer("PW" + strings.ToUpper(settled.Zones["main"].Power))

		api.refuseStatus(t, 2*statusDebounce)
	})
}

func TestARemovedReceiverStopsItsClient(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", equipment.address()))
		operator := startController(t, api)
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)

		api.setReceivers()
		mustSucceed(t, operator.pass(t.Context()))
		equipment.turnKnob(130)

		api.refuseStatus(t, 2*statusDebounce)
	})
}

func TestAChangedAddressGetsANewConnection(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		first, second := startFakeDenon(t), startFakeDenon(t)
		api.setReceivers(testReceiver("theater", first.address()))
		operator := startController(t, api)
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)

		api.setReceivers(testReceiver("theater", second.address()))
		mustSucceed(t, operator.pass(t.Context()))

		mustMatch(t, second.waitForCommand(t), "PW?")
	})
}

func TestAReceiverWithNoProtocolStartsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		api.setReceivers(Receiver{Metadata: ObjectMeta{Name: "theater", Generation: 1}})
		operator := startController(t, api)

		mustSucceed(t, operator.pass(t.Context()))

		api.refuseStatus(t, 2*statusDebounce)
	})
}

func TestPassReturnsTheErrorWhenTheListFails(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		api.breakTheList()
		operator := startController(t, api)

		mustFail(t, operator.pass(t.Context()))
	})
}

// sessionedReceiver returns a Receiver whose spec.session selects input
// and is active, as it is while a Play runs.
func sessionedReceiver(name, address, input string) Receiver {
	held := testReceiver(name, address)
	held.Spec.Session = &ReceiverSession{
		Player: "house/theater",
		Input:  input,
		Active: true,
	}
	return held
}

// A new session selects its input, a session on another input selects
// that one, and removing the session sends nothing further.
func TestASessionThatChangesSelectsTheNewInput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", equipment.address()))
		operator := startController(t, api)
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)

		api.setReceivers(sessionedReceiver("theater", equipment.address(), "GAME"))
		mustSucceed(t, operator.pass(t.Context()))
		equipment.waitForCommands(t, "SIGAME")

		api.setReceivers(sessionedReceiver("theater", equipment.address(), "TV"))
		mustSucceed(t, operator.pass(t.Context()))
		equipment.waitForCommands(t, "SITV")

		api.setReceivers(testReceiver("theater", equipment.address()))
		mustSucceed(t, operator.pass(t.Context()))
	})
}

// A session that a later pass reads unchanged is left alone, because
// the session sends its power and input commands once.
func TestASessionThatHasNotChangedIsLeftAlone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", equipment.address()))
		operator := startController(t, api)
		mustSucceed(t, operator.pass(t.Context()))

		api.setReceivers(sessionedReceiver("theater", equipment.address(), "GAME"))
		mustSucceed(t, operator.pass(t.Context()))
		equipment.waitForCommands(t, "SIGAME")
		mustSucceed(t, operator.pass(t.Context()))

		equipment.turnKnob(120)
		equipment.refuseCommand(t, "SIGAME", 4*statusDebounce)
	})
}

// The loop runs a pass before any event arrives, runs another on a
// wake, and stops every unit when its context ends.
func TestTheLoopRunsUntilItsContextEndsAndStopsEveryUnit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", equipment.address()))
		operator := startController(t, api)
		operator.networkDiscoveryOff = true

		ctx, cancel := context.WithCancel(t.Context())
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			operator.run(ctx)
		}()

		api.waitForStatus(t, connected)
		sendWake(operator.wake)
		cancel()

		select {
		case <-stopped:
		case <-time.After(testTimeout):
			t.Fatal("the loop was still running after the context ended, want it stopped")
		}
		mustMatch(t, len(operator.units), 0)
	})
}

// A status write that the API server refuses is not recorded as
// applied, so the next change writes the whole status again.
func TestARefusedStatusWriteIsTriedAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		api.breakTheStatus(true)
		api.setReceivers(testReceiver("theater", equipment.address()))
		operator := startController(t, api)

		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, func(status ReceiverStatus) bool { return len(status.Conditions) == 0 })

		api.breakTheStatus(false)
		equipment.turnKnob(120)

		mustMatch(t, api.waitForStatus(t, connected).Zones["main"].Volume, "60")
	})
}

// serve lists the Receivers once before it runs the loop, and returns
// the error when that list fails.
func TestServeReturnsTheErrorWhenTheFirstListFails(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		api.breakTheList()

		mustFail(t, serve(t.Context(), api.client, settings{namespace: testNamespace, dial: testNetwork.dial, listen: testNetwork.bind}, testMetrics(t)))
	})
}

func TestServeRunsTheLoopUntilItsContextEnds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", equipment.address()))

		ctx, cancel := context.WithCancel(t.Context())
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			mustSucceed(t, serve(ctx, api.client, settings{namespace: testNamespace, networkDiscoveryOff: true, dial: testNetwork.dial, listen: testNetwork.bind}, testMetrics(t)))
		}()

		api.waitForStatus(t, connected)
		cancel()

		select {
		case <-stopped:
		case <-time.After(testTimeout):
			t.Fatal("serve was still running after the context ended, want it stopped")
		}
	})
}

// A Play that starts on a waking screen sets both flags in one write.
// The session then selects the input once, without waiting.
func TestOneWriteThatTurnsBothFlagsOnSelectsTheInputOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		operator := newController(api.client, testMetrics(t))
		operator.dial = testNetwork.dial
		operator.listen = testNetwork.bind
		operator.now = func() time.Time { return statusNow }

		rule := ReceiverVolume{Max: 69.5, Step: 1}
		api.setReceivers(idleReceiver(equipment.address(), rule))
		mustSucceed(t, operator.pass(t.Context()))
		waitForFirstRead(t, operator)

		woken := playingReceiver(equipment.address(), rule)
		woken.Spec.Session.Awake = true
		api.setReceivers(woken)
		flipped := time.Now()
		mustSucceed(t, operator.pass(t.Context()))

		equipment.waitForCommands(t, "SIGAME")
		mustMatch(t, time.Since(flipped) < sessionPowerWait/2, true)
		equipment.refuseCommand(t, "SIGAME", 2*sessionPowerWait)
	})
}

// playingReceiver returns a Receiver on a Denon with an active session,
// as while a Play runs, and a declared volume scale.
func playingReceiver(address string, rule ReceiverVolume) Receiver {
	held := idleReceiver(address, rule)
	held.Spec.Session.Active = true
	return held
}

// idleReceiver returns the same Receiver with the Player at its idle
// screen. Its session is not active, so it sends the receiver nothing.
func idleReceiver(address string, rule ReceiverVolume) Receiver {
	held := testReceiver("theater", address)
	held.Spec.Volume = &rule
	held.Spec.Session = &ReceiverSession{
		Player: "house/theater",
		Input:  "GAME",
	}
	return held
}

// runningSession returns the unit's session. The pointer stays the same
// across a change that does not restart the session.
func runningSession(t *testing.T, operator *controller, name string) *session {
	t.Helper()
	unit, running := operator.units[name]
	if !running {
		t.Fatalf("no unit runs for Receiver %s, want one", name)
	}
	unit.mutex.Lock()
	defer unit.mutex.Unlock()
	return unit.session
}

// The media operator writes a session whenever the Player has a screen,
// and sets active when a Play starts. That change must reach the
// running session and not start another.
func TestAnActiveFlipReachesTheRunningSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		operator := newController(api.client, testMetrics(t))
		operator.dial = testNetwork.dial
		operator.listen = testNetwork.bind
		operator.now = func() time.Time { return statusNow }
		rule := ReceiverVolume{Max: 69.5, Step: 1}

		api.setReceivers(idleReceiver(equipment.address(), rule))
		mustSucceed(t, operator.pass(t.Context()))
		waitForFirstRead(t, operator)
		equipment.refuseCommand(t, "SIGAME", quietPeriod)
		before := runningSession(t, operator, "theater")

		api.setReceivers(playingReceiver(equipment.address(), rule))
		mustSucceed(t, operator.pass(t.Context()))

		equipment.waitForCommands(t, "SIGAME")
		mustMatch(t, runningSession(t, operator, "theater"), before)
	})
}

func TestInputSoundModeReadsTheDeclaredInputs(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		unit := &receiverUnit{}
		unit.setInputs([]ReceiverInput{{Name: "MPLAY", SoundMode: "STEREO"}})

		mustMatch(t, unit.inputSoundMode("MPLAY"), "STEREO")
		mustMatch(t, unit.inputSoundMode("GAME"), "")

		empty := &receiverUnit{}
		mustMatch(t, empty.inputSoundMode("MPLAY"), "")
	})
}

// A spec.power change that arrives while the operator runs is sent
// once, after the receiver is reachable and has finished its first full read. A later list with
// the same value sends nothing, because the operator owns the field and
// does not re-assert it.
func TestADeclarativePowerChangeAppliesOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		receiver := testReceiver("theater", fake.address())
		api.setReceivers(receiver)
		operator := startController(t, api)
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForFirstRead(t, operator)

		// After the first full read, the change is sent once.
		receiver.Spec.Power = equipment.PowerOn
		receiver.Metadata.Generation = 5
		api.setReceivers(receiver)
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, denon.PowerOnCommand)
		mustMatch(t, <-api.powers, equipment.PowerOn)

		// A list with the same value sends nothing more.
		mustSucceed(t, operator.pass(t.Context()))
		fake.refuseCommand(t, denon.PowerOnCommand, quietPeriod)
		select {
		case power := <-api.powers:
			t.Fatalf("the operator applied power %q again, want no second write for an unchanged list", power)
		case <-time.After(quietPeriod):
		}
	})
}

// A declared setting that the receiver reports at another value is sent
// once the receiver is reachable. After the receiver reports the
// declared value, a later list with the same settings sends nothing.
func TestADeclarativeSettingChangeAppliesOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		receiver := testReceiver("theater", fake.address())
		bass := 3
		receiver.Spec.Denon.Settings = denon.Settings{Tone: denon.ToneSettings{Bass: &bass}}
		api.setReceivers(receiver)
		operator := startController(t, api)

		// The first pass starts the driver, which connects in the
		// background, so the change is not sent until the receiver is
		// reachable.
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForFirstRead(t, operator)
		fake.holdSetting("PSBAS 53", "PSBAS 53")
		fake.volunteer("PSBAS 50")
		waitForObservedSettings(t, operator, "theater", func(s denon.Settings) bool {
			return s.Tone.Bass != nil && *s.Tone.Bass == 0
		})

		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "PSBAS 53")
		waitForObservedSettings(t, operator, "theater", func(s denon.Settings) bool {
			return *s.Tone.Bass == 3
		})

		mustSucceed(t, operator.pass(t.Context()))
		fake.refuseCommand(t, "PSBAS 53", quietPeriod)
	})
}

// Declared controls that a non-main zone reports at other values are
// sent as the matching commands for that zone. After the zone reports
// them, a later list with the same zones sends nothing.
func TestDeclaredZoneControlsApplyOnce(t *testing.T) {
	cases := []struct {
		name    string
		zone    string
		power   equipment.Power
		input   string
		volume  float64
		mute    bool
		sleep   int
		before  []string
		want    []string
		reports []string
	}{
		{"zone2", "zone2", equipment.PowerOn, "CD", 40, true, 30,
			[]string{"Z2OFF", "Z2PHONO", "Z220", "Z2MUOFF", "Z2SLPOFF"},
			[]string{"Z2ON", "Z2CD", "Z2MV40", "Z2MUON", "Z2SLP030"},
			[]string{"Z2ON", "Z2CD", "Z240", "Z2MUON", "Z2SLP030"}},
		{"zone3", "zone3", equipment.PowerStandby, "TV", 30, false, 30,
			[]string{"Z3ON", "Z3PHONO", "Z320", "Z3MUON", "Z3SLPOFF"},
			[]string{"Z3OFF", "Z3TV", "Z3MV30", "Z3MUOFF", "Z3SLP030"},
			[]string{"Z3OFF", "Z3TV", "Z330", "Z3MUOFF", "Z3SLP030"}},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := startFakeAPI(t)
				fake := startFakeDenon(t)
				receiver := testReceiver("theater", fake.address())
				receiver.Spec.Zones = map[string]ZoneSpec{
					one.zone: {Power: one.power, Input: one.input, Volume: &one.volume, Mute: &one.mute, Sleep: &one.sleep},
				}
				api.setReceivers(receiver)
				operator := startController(t, api)

				mustSucceed(t, operator.pass(t.Context()))
				api.waitForStatus(t, connected)
				waitForFirstRead(t, operator)
				for index, command := range one.want {
					fake.holdSetting(command, one.reports[index])
				}
				fake.volunteer(one.before...)
				waitForObservedZone(t, operator, "theater", one.zone, 40)

				mustSucceed(t, operator.pass(t.Context()))
				for _, want := range one.want {
					fake.waitForCommands(t, want)
				}
				// The sleep is the last report of the burst, so when it
				// arrives the receiver has reported every control.
				waitFor(t, func() bool {
					zone, _ := operator.units["theater"].driver.State().Zone(one.zone)
					return zone.Volume == int(one.volume*2) && zone.Sleep == one.sleep
				})

				mustSucceed(t, operator.pass(t.Context()))
				fake.refuseAnySet(t, quietPeriod)
			})
		})
	}
}

// setZones skips a main entry in spec.zones, because spec.power and the
// session control the main zone.
func TestAZonesMapThatNamesMainIsSkipped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		receiver := testReceiver("theater", fake.address())
		receiver.Spec.Zones = map[string]ZoneSpec{"main": {Power: equipment.PowerOn}}
		api.setReceivers(receiver)
		operator := startController(t, api)

		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForFirstRead(t, operator)
		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, "PWON", quietPeriod)
	})
}

// zonesApplied returns a copy and setZones stores a new map, so a
// snapshot taken before a later setZones keeps its earlier contents.
func TestAZoneSnapshotKeepsItsContentsAfterALaterSetZones(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		receiver := testReceiver("theater", fake.address())
		volume := 30.0
		receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Volume: &volume}}
		api.setReceivers(receiver)
		operator := startController(t, api)

		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForFirstRead(t, operator)
		fake.holdSetting("Z2MV30", "Z230")
		fake.volunteer("Z220")
		waitForObservedZone(t, operator, "theater", "zone2", 40)
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "Z2MV30")

		unit := operator.units["theater"]
		snapshot, _ := unit.zonesApplied()

		// A later setZones drives a second zone. It must not change the
		// snapshot taken before it.
		receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Volume: &volume}, "zone3": {Power: equipment.PowerOn}}
		api.setReceivers(receiver)
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "Z3ON")

		if _, held := snapshot["zone3"]; held {
			t.Errorf("the earlier snapshot holds zone3 after a later setZones, want it unchanged")
		}
		mustMatch(t, len(snapshot), 1)
		mustMatch(t, *snapshot["zone2"].Volume, volume)
	})
}

// A declared setting that the receiver cannot take is logged and not
// recorded as applied, so the next pass tries again, and a later valid
// change is sent.
func TestADeclarativeSettingThatFailsToApplyIsRetried(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		receiver := testReceiver("theater", fake.address())
		eco := "turbo"
		receiver.Spec.Denon.Settings = denon.Settings{System: denon.SystemSettings{Eco: &eco}}
		api.setReceivers(receiver)
		operator := startController(t, api)

		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForFirstRead(t, operator)
		mustSucceed(t, operator.pass(t.Context()))

		// ApplySettings returns an error for a value that no command
		// can carry, so nothing is recorded and the value stays
		// pending.
		bass := 3
		receiver.Spec.Denon.Settings = denon.Settings{Tone: denon.ToneSettings{Bass: &bass}}
		api.setReceivers(receiver)
		mustSucceed(t, operator.pass(t.Context()))

		fake.waitForCommands(t, "PSBAS 53")
	})
}

// A zone control that the receiver cannot take is logged and not
// recorded as applied, so the next pass tries again, and a later valid
// change is sent.
func TestAZoneControlThatFailsToApplyIsRetried(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		receiver := testReceiver("theater", fake.address())
		sleep := 200
		receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Sleep: &sleep}}
		api.setReceivers(receiver)
		operator := startController(t, api)

		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForFirstRead(t, operator)
		mustSucceed(t, operator.pass(t.Context()))

		// SetSleep rejects a timer past the receiver's range, so
		// nothing is recorded and the value stays pending.
		volume := 40.0
		receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Volume: &volume}}
		api.setReceivers(receiver)
		mustSucceed(t, operator.pass(t.Context()))

		fake.waitForCommands(t, "Z2MV40")
	})
}

// waitForObservedSettings waits until the driver's reported settings
// satisfy check, so the receiver's echo has reached the driver before
// the next pass reads it.
func waitForObservedSettings(t *testing.T, operator *controller, name string, check func(denon.Settings) bool) {
	t.Helper()
	unit := operator.units[name]
	deadline := time.After(testTimeout)
	for {
		if check(unit.denonClient.Settings()) {
			return
		}
		select {
		case <-deadline:
			t.Fatal("the receiver reported none of the settings the test wants before the deadline")
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// waitForObservedZone waits until the driver reports the given volume
// for a zone, in the driver's steps.
func waitForObservedZone(t *testing.T, operator *controller, name, zone string, volume int) {
	t.Helper()
	unit := operator.units[name]
	deadline := time.After(testTimeout)
	for {
		if state, reported := unit.driver.State().Zone(zone); reported && state.Volume == volume {
			return
		}
		select {
		case <-deadline:
			t.Fatal("the receiver did not report the wanted zone volume before the deadline")
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// A declared setting that the receiver accepts but does not apply is
// sent again on each pass until the receiver reports the declared
// value. The operator then sends once more and stops.
func TestASettingTheReceiverIgnoresIsRetriedUntilReported(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		receiver := testReceiver("theater", fake.address())
		eco := "on"
		receiver.Spec.Denon.Settings = denon.Settings{System: denon.SystemSettings{Eco: &eco}}
		api.setReceivers(receiver)
		operator := startController(t, api)

		// The first pass starts the driver, which connects in the
		// background, so the change is not sent until the receiver is
		// reachable.
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForFirstRead(t, operator)

		// The receiver is in standby, so it takes the eco command but
		// keeps reporting the standby value.
		fake.holdSetting("ECOON", "ECOOFF")
		fake.volunteer("ECOOFF")
		waitForObservedSettings(t, operator, "theater", func(s denon.Settings) bool {
			return s.System.Eco != nil && *s.System.Eco == "off"
		})

		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "ECOON")
		waitForObservedSettings(t, operator, "theater", func(s denon.Settings) bool {
			return s.System.Eco != nil && *s.System.Eco == "off"
		})

		// The receiver has still not reported the declared value, so
		// the operator sends the command again on the next pass.
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "ECOON")

		// Once the receiver reports the declared value, the operator
		// sends once more and then stops.
		fake.holdSetting("ECOON", "ECOON")
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "ECOON")
		waitForObservedSettings(t, operator, "theater", func(s denon.Settings) bool {
			return s.System.Eco != nil && *s.System.Eco == "on"
		})
		mustSucceed(t, operator.pass(t.Context()))
		fake.refuseCommand(t, "ECOON", quietPeriod)
	})
}

// A declared zone control that the receiver accepts but does not apply
// is sent again on each pass until the zone reports the declared value.
// The operator then sends once more and stops.
func TestAZoneTheReceiverIgnoresIsRetriedUntilReported(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		receiver := testReceiver("theater", fake.address())
		volume := 40.0
		receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Volume: &volume}}
		api.setReceivers(receiver)
		operator := startController(t, api)

		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForFirstRead(t, operator)

		// The zone takes the volume command but keeps reporting its old
		// volume, so the operator sends it again until the zone reports
		// the declared level. The set command is Z2MV40, and the
		// receiver reports a volume as Z2 followed by the digits.
		fake.holdSetting("Z2MV40", "Z230")
		fake.volunteer("Z230")
		waitForObservedZone(t, operator, "theater", "zone2", 60)

		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "Z2MV40")

		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "Z2MV40")

		// The zone starts reporting the declared volume, so the
		// operator sends once more and then stops.
		fake.holdSetting("Z2MV40", "Z240")
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "Z2MV40")
		waitForObservedZone(t, operator, "theater", "zone2", 80)
		mustSucceed(t, operator.pass(t.Context()))
		fake.refuseCommand(t, "Z2MV40", quietPeriod)
	})
}
