package main

// The controller against a fake receiver and a fake API server, both on
// in-memory connections, on the fake clock of a synctest bubble.

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

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

func TestPokeNeverBlocksAndDrainPokesClearsTheQueue(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wake := make(chan struct{}, 1)

		poke(wake)
		poke(wake)
		mustMatch(t, len(wake), 1)

		drainPokes(wake)
		mustMatch(t, len(wake), 0)
	})
}

// fakeAPI is an API server that answers the collection a test sets and
// records every status the operator applies.
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
	// lists counts the lists of the Receivers.
	lists int
	// applied names the Receiver of each apply on the main resource.
	applied []string
	// throttlingStatus answers every status write with a 429 that asks
	// for a five-second wait, and statusThrottles counts those answers.
	throttlingStatus bool
	statusThrottles  int
}

func startFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	api := &fakeAPI{written: make(chan ReceiverStatus, 64), powers: make(chan equipment.Power, 64), events: &eventstest.Events{}}
	api.client = testAPIClient(t, api.events.Around(http.HandlerFunc(api.handle)))
	return api
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

// The watch answers a streaming list with the Receivers a test set,
// the way the API server does, and then holds the connection open.
// This fake sends no later event: a test runs each pass itself.
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

// recordPower answers the operator's apply on the main resource, which
// owns spec.power, and records the power the body named.
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

// appliedReceivers names the Receiver of each apply on the main
// resource, in order.
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

// gatePowers makes every power write wait until releasePowers, so a
// test can hold a handler open at its API patch.
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

// lastStatus answers the last status the operator applied.
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

// waitForStatus reads applied statuses until one satisfies ready.
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
			t.Fatal("the operator applied no status the test was waiting for")
			return ReceiverStatus{}
		}
	}
}

// refuseStatus fails the test if any status is applied within the
// window.
func (a *fakeAPI) refuseStatus(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case status := <-a.written:
		t.Fatalf("the operator applied a status it did not need to: %+v", status)
	case <-time.After(within):
	}
}

func testReceiver(name, address string) Receiver {
	return Receiver{
		Metadata: ObjectMeta{Name: name, Generation: 4},
		Spec:     ReceiverSpec{Denon: &DenonProtocol{Address: address}},
	}
}

// startController leaves statusDebounce at its own value, because a
// unit's writer goroutine outlives the test and would race a restore.
func startController(t *testing.T, api *fakeAPI) *controller {
	t.Helper()
	operator := newController(api.client, testMetrics(t))
	operator.dial = testNetwork.dial
	operator.now = func() time.Time { return statusNow }
	operator.recorder = testRecorder(t, api.client)
	return operator
}

// waitForSurvey waits until every unit the operator has reached has
// surveyed its receiver, which is what gates the settings apply. A unit
// the operator has not reached is skipped: it has nothing to compare.
func waitForSurvey(t *testing.T, operator *controller) {
	t.Helper()
	deadline := time.After(4 * time.Second)
	for {
		waiting := false
		for _, unit := range operator.units {
			if unit.driver == nil {
				continue
			}
			if unit.driver.State().Reachable == equipment.ConditionTrue && !unit.driver.Surveyed() {
				waiting = true
			}
		}
		if !waiting {
			return
		}
		select {
		case <-deadline:
			t.Fatal("a connected driver never surveyed its receiver")
		case <-time.After(time.Millisecond):
		}
	}
}

// connected answers whether a status says Reachable is True and holds
// no condition other than the InputSelected that a standing session
// adds.
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

// The connect answers nine lines this operator reads, and they make one
// status write.
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
		// The first pass after the survey settles the declared blocks and
		// records them, which is a write. The pass after that has nothing
		// left to write.
		waitForSurvey(t, operator)
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, func(status ReceiverStatus) bool { return len(status.SettledSettings) > 0 })

		mustSucceed(t, operator.pass(t.Context()))

		api.refuseStatus(t, 2*statusDebounce)
	})
}

// A receiver repeats a line whenever its own menu or remote touches a
// field, often with the value it already reported. The burst that line
// starts builds the same status, so the operator writes nothing.
func TestALineThatRepeatsTheStateWritesNoStatus(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", equipment.address()))
		operator := startController(t, api)
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForSurvey(t, operator)
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

func TestPassAnswersTheErrorWhenTheListFails(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		api.breakTheList()
		operator := startController(t, api)

		mustFail(t, operator.pass(t.Context()))
	})
}

// sessionedReceiver is a Receiver that names a session on one input,
// with a Play standing on it.
func sessionedReceiver(name, address, input string) Receiver {
	held := testReceiver(name, address)
	held.Spec.Session = &ReceiverSession{
		Player: "house/theater",
		Input:  input,
		Active: true,
	}
	return held
}

// A session that appears selects the input, a session under a new input
// selects that one, and a session that is lifted selects nothing
// further.
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

// A session the pass reads again is left alone, because power and input
// are one-shots the receiver answers once.
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

// The loop reconciles before any event arrives, answers a wake, and
// stops every unit when its context ends.
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
		poke(operator.wake)
		cancel()

		select {
		case <-stopped:
		case <-time.After(testTimeout):
			t.Fatal("the loop did not stop")
		}
		mustMatch(t, len(operator.units), 0)
	})
}

// A status write the API server refuses is not recorded, so the next
// change writes the whole status again instead of skipping it as
// already applied.
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

// serve reads the collection once before it runs, and answers the error
// when that read fails.
func TestServeAnswersTheErrorWhenTheFirstListFails(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		api.breakTheList()

		mustFail(t, serve(t.Context(), api.client, settings{dial: testNetwork.dial}, testMetrics(t)))
	})
}

// serve runs the loop until its context ends.
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
			mustSucceed(t, serve(ctx, api.client, settings{networkDiscoveryOff: true, dial: testNetwork.dial}, testMetrics(t)))
		}()

		api.waitForStatus(t, connected)
		cancel()

		select {
		case <-stopped:
		case <-time.After(testTimeout):
			t.Fatal("serve did not stop")
		}
	})
}

// A Play that starts on a waking screen turns both flags on in one
// write, and the session selects the input once and at once.
func TestOneWriteThatTurnsBothFlagsOnSelectsTheInputOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		operator := newController(api.client, testMetrics(t))
		operator.dial = testNetwork.dial
		operator.now = func() time.Time { return statusNow }

		rule := ReceiverVolume{Max: 69.5, Step: 1}
		api.setReceivers(idleReceiver(equipment.address(), rule))
		mustSucceed(t, operator.pass(t.Context()))
		waitForSurvey(t, operator)

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

// playingReceiver is one Denon with a Play standing on it and a
// declared scale.
func playingReceiver(address string, rule ReceiverVolume) Receiver {
	held := idleReceiver(address, rule)
	held.Spec.Session.Active = true
	return held
}

// idleReceiver is the same receiver with the Player at its idle screen:
// a session that asks the equipment for nothing.
func idleReceiver(address string, rule ReceiverVolume) Receiver {
	held := testReceiver("theater", address)
	held.Spec.Volume = &rule
	held.Spec.Session = &ReceiverSession{
		Player: "house/theater",
		Input:  "GAME",
	}
	return held
}

// heldSession answers the session a unit runs now, which is the same
// pointer across a change that does not restart the session.
func heldSession(t *testing.T, operator *controller, name string) *session {
	t.Helper()
	unit, running := operator.units[name]
	if !running {
		t.Fatalf("no unit is running for receiver %s", name)
	}
	unit.mutex.Lock()
	defer unit.mutex.Unlock()
	return unit.session
}

// The media operator holds the session whenever the Player has a
// screen, and flips active when a Play starts. The flip must reach the
// session that stands, and not start another.
func TestAnActiveFlipReachesAStandingSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		equipment := startFakeDenon(t)
		operator := newController(api.client, testMetrics(t))
		operator.dial = testNetwork.dial
		operator.now = func() time.Time { return statusNow }
		rule := ReceiverVolume{Max: 69.5, Step: 1}

		api.setReceivers(idleReceiver(equipment.address(), rule))
		mustSucceed(t, operator.pass(t.Context()))
		waitForSurvey(t, operator)
		equipment.refuseCommand(t, "SIGAME", quietPeriod)
		before := heldSession(t, operator, "theater")

		api.setReceivers(playingReceiver(equipment.address(), rule))
		mustSucceed(t, operator.pass(t.Context()))

		equipment.waitForCommands(t, "SIGAME")
		mustMatch(t, heldSession(t, operator, "theater"), before)
	})
}

// The sound-mode lookup reads the declared inputs and answers nothing
// for an input the wiring does not name.
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

// A declarative spec.power change the operator sees while it runs is
// applied once the receiver is reachable and surveyed, and a re-list
// with the same value sends nothing further: the operator owns the
// field and never re-asserts it.
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
		waitForSurvey(t, operator)

		// Once surveyed, the change applies once.
		receiver.Spec.Power = equipment.PowerOn
		receiver.Metadata.Generation = 5
		api.setReceivers(receiver)
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, denon.PowerOnCommand)
		mustMatch(t, <-api.powers, equipment.PowerOn)

		// A re-list with the same value sends nothing.
		mustSucceed(t, operator.pass(t.Context()))
		fake.refuseCommand(t, denon.PowerOnCommand, quietPeriod)
		select {
		case power := <-api.powers:
			t.Fatalf("the operator re-applied power %q", power)
		case <-time.After(quietPeriod):
		}
	})
}

// A declared setting the receiver reports at another value is applied
// once the receiver is reachable, and once the receiver reports the
// declared value, a re-list with the same settings sends nothing.
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

		// The first pass starts the driver, which connects asynchronously, so
		// the change waits for a reachable receiver.
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForSurvey(t, operator)
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

// Declared zone controls the zone reports at other values reach the
// right wire lines for each non-main zone, and once the zone reports
// them, a re-list with the same zones sends nothing further.
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
				waitForSurvey(t, operator)
				for index, command := range one.want {
					fake.holdSetting(command, one.reports[index])
				}
				fake.volunteer(one.before...)
				waitForObservedZone(t, operator, "theater", one.zone, 40)

				mustSucceed(t, operator.pass(t.Context()))
				for _, want := range one.want {
					fake.waitForCommands(t, want)
				}
				// The sleep is the last report of the burst, so once it arrives
				// the receiver has reported every control.
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

// A zones map that names the main zone is rejected, so the main zone
// never has two writers: spec.power and spec.zones.
func TestAZonesMapThatNamesMainIsRejected(t *testing.T) {
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
		waitForSurvey(t, operator)
		mustSucceed(t, operator.pass(t.Context()))

		fake.refuseCommand(t, "PWON", quietPeriod)
	})
}

// zonesApplied returns a copy and setZones stores a fresh map, so a
// snapshot read before a later setZones keeps the earlier value; the two
// never share a backing map.
func TestASettledZoneSnapshotSurvivesALaterSetZones(t *testing.T) {
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
		waitForSurvey(t, operator)
		fake.holdSetting("Z2MV30", "Z230")
		fake.volunteer("Z220")
		waitForObservedZone(t, operator, "theater", "zone2", 40)
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "Z2MV30")

		unit := operator.units["theater"]
		snapshot, _ := unit.zonesApplied()

		// A later setZones drives a second zone and must not reach back into
		// the snapshot read before it.
		receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Volume: &volume}, "zone3": {Power: equipment.PowerOn}}
		api.setReceivers(receiver)
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "Z3ON")

		if _, held := snapshot["zone3"]; held {
			t.Errorf("a later setZones mutated an earlier snapshot")
		}
		mustMatch(t, len(snapshot), 1)
		mustMatch(t, *snapshot["zone2"].Volume, volume)
	})
}

// A declared setting the receiver cannot carry is logged and not
// recorded, so the next pass tries again; a later valid change lands.
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
		waitForSurvey(t, operator)
		mustSucceed(t, operator.pass(t.Context()))

		// ApplySettings errors on the word no command can carry, so nothing
		// is recorded and the value stays pending.
		bass := 3
		receiver.Spec.Denon.Settings = denon.Settings{Tone: denon.ToneSettings{Bass: &bass}}
		api.setReceivers(receiver)
		mustSucceed(t, operator.pass(t.Context()))

		fake.waitForCommands(t, "PSBAS 53")
	})
}

// A zone control the receiver cannot carry is logged and not recorded,
// so the next pass tries again; a later valid change lands.
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
		waitForSurvey(t, operator)
		mustSucceed(t, operator.pass(t.Context()))

		// SetSleep rejects the timer past the receiver's range, so nothing
		// is recorded and the value stays pending.
		volume := 40.0
		receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Volume: &volume}}
		api.setReceivers(receiver)
		mustSucceed(t, operator.pass(t.Context()))

		fake.waitForCommands(t, "Z2MV40")
	})
}

// waitForObservedSettings waits for the driver's reported settings to
// satisfy check, which is the receiver's echo reaching the state before
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
			t.Fatal("the receiver never reported the settings the test expected")
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// waitForObservedZone waits for the driver to report one zone's
// volume, in the driver's steps.
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
			t.Fatal("the receiver never reported the zone volume the test expected")
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// A declared setting the receiver accepts but does not apply is sent
// again on the next pass until the receiver reports the declared value,
// then the operator stops.
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

		// The first pass starts the driver, which connects asynchronously, so
		// the change waits for a reachable receiver.
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)
		waitForSurvey(t, operator)

		// The receiver is in standby, so it takes the eco command but keeps
		// reporting the standby value.
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

		// The receiver still has not reported the declared value, so the
		// operator sends the command again on the next pass.
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "ECOON")

		// Once the receiver reports the declared value, the operator sends
		// once more and then stops.
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

// A declared zone control the receiver accepts but does not apply is
// sent again on the next pass until the zone reports the declared
// value, then the operator stops.
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
		waitForSurvey(t, operator)

		// The zone takes the volume command but keeps reporting its old
		// volume, so the operator re-sends until the zone reports the
		// declared level. The set command is Z2MV40, and the receiver
		// reports a volume as Z2 followed by the digits.
		fake.holdSetting("Z2MV40", "Z230")
		fake.volunteer("Z230")
		waitForObservedZone(t, operator, "theater", "zone2", 60)

		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "Z2MV40")

		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "Z2MV40")

		// The zone starts reporting the declared volume, so the operator
		// sends once more and then stops.
		fake.holdSetting("Z2MV40", "Z240")
		mustSucceed(t, operator.pass(t.Context()))
		fake.waitForCommands(t, "Z2MV40")
		waitForObservedZone(t, operator, "theater", "zone2", 80)
		mustSucceed(t, operator.pass(t.Context()))
		fake.refuseCommand(t, "Z2MV40", quietPeriod)
	})
}
