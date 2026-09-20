package main

// The controller against a fake receiver and a fake API server, both on
// real sockets.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

func TestPokeNeverBlocksAndDrainPokesClearsTheQueue(t *testing.T) {
	wake := make(chan struct{}, 1)

	poke(wake)
	poke(wake)
	mustMatch(t, len(wake), 1)

	drainPokes(wake)
	mustMatch(t, len(wake), 0)
}

// fakeAPI is an API server that answers the collection a test sets and
// records every status the operator applies.
type fakeAPI struct {
	client   *Client
	written  chan ReceiverStatus
	watched  chan string
	powers   chan equipment.Power
	settings chan []byte

	mutex        sync.Mutex
	list         ReceiverList
	broken       bool
	statuses     []ReceiverStatus
	powersSet    []equipment.Power
	settingsSet  [][]byte
	refusing     bool
	events       []string
	endStream    bool
	settingsGate chan struct{}
}

func startFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	api := &fakeAPI{written: make(chan ReceiverStatus, 64), watched: make(chan string, 8), powers: make(chan equipment.Power, 64), settings: make(chan []byte, 64)}
	api.client = testAPIClient(t, http.HandlerFunc(api.handle))
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
	broken, list := a.broken, a.list
	a.mutex.Unlock()
	if broken {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(list)
}

// The watch writes the events a test queued and then holds the
// connection open, the way the API server holds one, unless the test
// asked for a stream that ends.
func (a *fakeAPI) serveWatch(w http.ResponseWriter, r *http.Request) {
	a.mutex.Lock()
	events, ends := a.events, a.endStream
	a.mutex.Unlock()

	select {
	case a.watched <- r.URL.Query().Get("resourceVersion"):
	default:
	}
	for _, event := range events {
		_, _ = io.WriteString(w, event+"\n")
	}
	w.(http.Flusher).Flush()
	if ends {
		return
	}
	<-r.Context().Done()
}

// queueWatchEvents states what every watch on this server writes before
// it holds or ends.
func (a *fakeAPI) queueWatchEvents(ends bool, events ...string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.events, a.endStream = events, ends
}

func (a *fakeAPI) recordStatus(w http.ResponseWriter, r *http.Request) {
	a.mutex.Lock()
	refusing := a.refusing
	a.mutex.Unlock()
	if refusing {
		select {
		case a.written <- ReceiverStatus{}:
		default:
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var applied receiverStatusApply
	_ = json.NewDecoder(r.Body).Decode(&applied)
	a.mutex.Lock()
	a.statuses = append(a.statuses, applied.Status)
	a.mutex.Unlock()
	select {
	case a.written <- applied.Status:
	default:
	}
	_ = json.NewEncoder(w).Encode(&Receiver{Metadata: applied.Metadata, Status: applied.Status})
}

// recordPower answers the operator's apply on the main resource, which
// owns spec.power or one leaf of spec.denon.settings, and records which
// one the body named. A body whose spec names a denon block is a
// settings leaf; anything else is a power.
func (a *fakeAPI) recordPower(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var applied receiverPowerApply
	_ = json.Unmarshal(body, &applied)
	if applied.Spec.Denon != nil {
		a.mutex.Lock()
		a.settingsSet = append(a.settingsSet, body)
		gate := a.settingsGate
		a.mutex.Unlock()
		select {
		case a.settings <- body:
		default:
		}
		if gate != nil {
			<-gate
		}
	} else {
		a.mutex.Lock()
		a.powersSet = append(a.powersSet, applied.Spec.Power)
		a.mutex.Unlock()
		select {
		case a.powers <- applied.Spec.Power:
		default:
		}
	}
	_ = json.NewEncoder(w).Encode(&Receiver{Metadata: applied.Metadata, Spec: applied.Spec})
}

func (a *fakeAPI) setReceivers(items ...Receiver) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.list = ReceiverList{Metadata: ListMeta{ResourceVersion: "1"}, Items: items}
}

// gateSettings makes every settings write wait until releaseSettings,
// so a test can hold a handler open at its API patch.
func (a *fakeAPI) gateSettings() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.settingsGate = make(chan struct{})
}

func (a *fakeAPI) releaseSettings() {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.settingsGate != nil {
		close(a.settingsGate)
		a.settingsGate = nil
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
	operator := newController(api.client, "127.0.0.1:1", testMetrics(t))
	operator.now = func() time.Time { return statusNow }
	return operator
}

func connected(status ReceiverStatus) bool {
	return len(status.Conditions) == 1 && status.Conditions[0].Status == ConditionTrue
}

func TestTheOperatorReportsWhatTheReceiverSaid(t *testing.T) {
	api := startFakeAPI(t)
	equipment := startFakeDenon(t)
	api.setReceivers(testReceiver("theater", equipment.address()))
	operator := startController(t, api)

	mustSucceed(t, operator.pass(t.Context()))

	status := api.waitForStatus(t, func(status ReceiverStatus) bool {
		return connected(status) && status.Zones["main"].SoundMode != ""
	})
	mustMatch(t, status.Zones["main"].Power, "standby")
	mustMatch(t, status.Zones["main"].Input, "MPLAY")
	mustMatch(t, status.Zones["main"].Volume, "50")
	mustMatch(t, status.Zones["main"].VolumeMax, "69.5")
	mustMatch(t, status.Zones["main"].Mute, false)
	mustMatch(t, status.Zones["main"].SoundMode, "MULTI CH IN")
	mustMatch(t, status.Service, "")
	mustMatch(t, status.Conditions[0].Reason, reasonConnected)
	mustMatch(t, status.Conditions[0].ObservedGeneration, int64(4))
}

// The connect answers nine lines this operator reads, and they make one
// status write.
func TestTheConnectBurstMakesOneStatusWrite(t *testing.T) {
	api := startFakeAPI(t)
	equipment := startFakeDenon(t)
	api.setReceivers(testReceiver("theater", equipment.address()))
	operator := startController(t, api)

	mustSucceed(t, operator.pass(t.Context()))

	api.waitForStatus(t, func(status ReceiverStatus) bool {
		return connected(status) && status.Zones["main"].SoundMode != ""
	})
	mustMatch(t, api.writeCount(), 1)
}

func TestABurstOfKnobTurnsMakesOneStatusWrite(t *testing.T) {
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
}

func TestASecondPassWritesNothingFurther(t *testing.T) {
	api := startFakeAPI(t)
	equipment := startFakeDenon(t)
	api.setReceivers(testReceiver("theater", equipment.address()))
	operator := startController(t, api)
	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)

	mustSucceed(t, operator.pass(t.Context()))

	api.refuseStatus(t, 2*statusDebounce)
}

func TestARemovedReceiverStopsItsClient(t *testing.T) {
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
}

func TestAChangedAddressGetsANewConnection(t *testing.T) {
	api := startFakeAPI(t)
	first, second := startFakeDenon(t), startFakeDenon(t)
	api.setReceivers(testReceiver("theater", first.address()))
	operator := startController(t, api)
	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)

	api.setReceivers(testReceiver("theater", second.address()))
	mustSucceed(t, operator.pass(t.Context()))

	mustMatch(t, second.waitForCommand(t), "PW?")
}

func TestAReceiverWithNoProtocolStartsNothing(t *testing.T) {
	api := startFakeAPI(t)
	api.setReceivers(Receiver{Metadata: ObjectMeta{Name: "theater", Generation: 1}})
	operator := startController(t, api)

	mustSucceed(t, operator.pass(t.Context()))

	api.refuseStatus(t, 2*statusDebounce)
}

func TestPassAnswersTheErrorWhenTheListFails(t *testing.T) {
	api := startFakeAPI(t)
	api.breakTheList()
	operator := startController(t, api)

	mustFail(t, operator.pass(t.Context()))
}

// sessionedReceiver is a Receiver that names a session on one input,
// with a Play standing on it.
func sessionedReceiver(name, address, input string) Receiver {
	held := testReceiver(name, address)
	held.Spec.Session = &ReceiverSession{
		Player:      "house/theater",
		Input:       input,
		VolumeTopic: "liken/media/players/house/theater/volume",
		Active:      true,
	}
	return held
}

// A session that appears selects the input, a session under a new input
// selects that one, and a session that is lifted selects nothing
// further.
func TestASessionThatChangesSelectsTheNewInput(t *testing.T) {
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
}

// A session the pass reads again is left alone, because power and input
// are one-shots the receiver answers once.
func TestASessionThatHasNotChangedIsLeftAlone(t *testing.T) {
	api := startFakeAPI(t)
	equipment := startFakeDenon(t)
	api.setReceivers(sessionedReceiver("theater", equipment.address(), "GAME"))
	operator := startController(t, api)

	mustSucceed(t, operator.pass(t.Context()))
	equipment.waitForCommands(t, "SIGAME")
	mustSucceed(t, operator.pass(t.Context()))

	equipment.turnKnob(120)
	equipment.refuseCommand(t, "SIGAME", 4*statusDebounce)
}

// The loop reconciles before any event arrives, answers a wake, and
// stops every unit when its context ends.
func TestTheLoopRunsUntilItsContextEndsAndStopsEveryUnit(t *testing.T) {
	api := startFakeAPI(t)
	equipment := startFakeDenon(t)
	api.setReceivers(testReceiver("theater", equipment.address()))
	operator := startController(t, api)

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
}

// A status write the API server refuses is not recorded, so the next
// change writes the whole status again instead of skipping it as
// already applied.
func TestARefusedStatusWriteIsTriedAgain(t *testing.T) {
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
}

// serve reads the collection once before it runs, and answers the error
// when that read fails.
func TestServeAnswersTheErrorWhenTheFirstListFails(t *testing.T) {
	api := startFakeAPI(t)
	api.breakTheList()

	mustFail(t, serve(t.Context(), api.client, "127.0.0.1:1", testMetrics(t)))
}

// serve runs the loop until its context ends.
func TestServeRunsTheLoopUntilItsContextEnds(t *testing.T) {
	api := startFakeAPI(t)
	equipment := startFakeDenon(t)
	api.setReceivers(testReceiver("theater", equipment.address()))
	api.queueWatchEvents(false)

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		mustSucceed(t, serve(ctx, api.client, "127.0.0.1:1", testMetrics(t)))
	}()

	api.waitForStatus(t, connected)
	cancel()

	select {
	case <-stopped:
	case <-time.After(testTimeout):
		t.Fatal("serve did not stop")
	}
}

// An edit to spec.volume reaches a session that is already standing, so
// the step a press takes changes without the receiver being powered or
// its input selected again.
func TestAVolumeEditReachesAStandingSession(t *testing.T) {
	api := startFakeAPI(t)
	equipment := startFakeDenon(t)
	brokers := startFakeBrokerServer(t)
	operator := newController(api.client, brokers.address(), testMetrics(t))
	operator.now = func() time.Time { return statusNow }

	api.setReceivers(playingReceiver(equipment.address(), ReceiverVolume{Max: 69.5, Step: 1}))
	mustSucceed(t, operator.pass(t.Context()))

	broker := brokers.waitForSession(t)
	broker.waitForTopic(t, ownerTopic(testVolumeTopic))
	adopted := broker.waitForTopic(t, testVolumeTopic)
	broker.push(testVolumeTopic, adopted.payload)
	waitUntilSessionAdopted(t, operator, "theater")
	equipment.waitForCommands(t, "SIGAME")

	broker.push(testVolumeTopic, []byte(`{"level":77,"muted":false}`))
	equipment.waitForCommands(t, "MV51")

	api.setReceivers(playingReceiver(equipment.address(), ReceiverVolume{Max: 69.5, Step: 2}))
	mustSucceed(t, operator.pass(t.Context()))
	equipment.refuseCommand(t, "SIGAME", quietPeriod)

	broker.push(testVolumeTopic, []byte(`{"level":85,"muted":false}`))
	equipment.waitForCommands(t, "MV53")
}

// A Play that starts on a waking screen turns both flags on in one
// write, and the session selects the input once and at once.
func TestOneWriteThatTurnsBothFlagsOnSelectsTheInputOnce(t *testing.T) {
	waited := sessionPowerWait
	sessionPowerWait = 500 * time.Millisecond
	t.Cleanup(func() { sessionPowerWait = waited })

	api := startFakeAPI(t)
	equipment := startFakeDenon(t)
	brokers := startFakeBrokerServer(t)
	operator := newController(api.client, brokers.address(), testMetrics(t))
	operator.now = func() time.Time { return statusNow }

	rule := ReceiverVolume{Max: 69.5, Step: 1}
	api.setReceivers(idleReceiver(equipment.address(), rule))
	mustSucceed(t, operator.pass(t.Context()))
	broker := brokers.waitForSession(t)
	broker.waitForTopic(t, ownerTopic(testVolumeTopic))
	adopted := broker.waitForTopic(t, testVolumeTopic)
	broker.push(testVolumeTopic, adopted.payload)
	waitUntilSessionAdopted(t, operator, "theater")

	woken := playingReceiver(equipment.address(), rule)
	woken.Spec.Session.Awake = true
	api.setReceivers(woken)
	flipped := time.Now()
	mustSucceed(t, operator.pass(t.Context()))

	equipment.waitForCommands(t, "SIGAME")
	mustMatch(t, time.Since(flipped) < sessionPowerWait/2, true)
	equipment.refuseCommand(t, "SIGAME", 2*sessionPowerWait)
}

// playingReceiver is one Denon with a Play standing on it and a
// declared scale, which is the whole of what a press needs.
func playingReceiver(address string, rule ReceiverVolume) Receiver {
	held := idleReceiver(address, rule)
	held.Spec.Session.Active = true
	return held
}

// idleReceiver is the same receiver with the Player at its idle screen:
// a session that owns the level and asks the equipment for nothing.
func idleReceiver(address string, rule ReceiverVolume) Receiver {
	held := testReceiver("theater", address)
	held.Spec.Volume = &rule
	held.Spec.Session = &ReceiverSession{
		Player:      "house/theater",
		Input:       "GAME",
		VolumeTopic: testVolumeTopic,
	}
	return held
}

// waitUntilSessionAdopted waits for a unit's session to take the
// broker's delivery of its own adopt message, which is the point from
// which a press moves the receiver.
func waitUntilSessionAdopted(t *testing.T, operator *controller, name string) {
	t.Helper()
	waitUntilAdopted(t, heldSession(t, operator, name))
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
// session that stands: the same connection, the same owner mark, and no
// second adopt.
func TestAnActiveFlipReachesAStandingSession(t *testing.T) {
	api := startFakeAPI(t)
	equipment := startFakeDenon(t)
	brokers := startFakeBrokerServer(t)
	operator := newController(api.client, brokers.address(), testMetrics(t))
	operator.now = func() time.Time { return statusNow }
	rule := ReceiverVolume{Max: 69.5, Step: 1}

	api.setReceivers(idleReceiver(equipment.address(), rule))
	mustSucceed(t, operator.pass(t.Context()))
	broker := brokers.waitForSession(t)
	broker.waitForTopic(t, ownerTopic(testVolumeTopic))
	adopted := broker.waitForTopic(t, testVolumeTopic)
	broker.push(testVolumeTopic, adopted.payload)
	waitUntilSessionAdopted(t, operator, "theater")
	equipment.refuseCommand(t, "SIGAME", quietPeriod)
	before := heldSession(t, operator, "theater")

	api.setReceivers(playingReceiver(equipment.address(), rule))
	mustSucceed(t, operator.pass(t.Context()))

	equipment.waitForCommands(t, "SIGAME")
	mustMatch(t, heldSession(t, operator, "theater"), before)
	broker.refuseTopic(t, ownerTopic(testVolumeTopic), quietPeriod)
	mustMatch(t, len(brokers.sessions), 0)
}

// The sound-mode lookup reads the declared inputs and answers nothing
// for an input the wiring does not name.
func TestInputSoundModeReadsTheDeclaredInputs(t *testing.T) {
	unit := &receiverUnit{}
	unit.setInputs([]ReceiverInput{{Name: "MPLAY", SoundMode: "STEREO"}})

	mustMatch(t, unit.inputSoundMode("MPLAY"), "STEREO")
	mustMatch(t, unit.inputSoundMode("GAME"), "")

	empty := &receiverUnit{}
	mustMatch(t, empty.inputSoundMode("MPLAY"), "")
}

// A declarative spec.power change is applied once the receiver is
// reachable, and a re-list with the same value sends nothing further:
// the operator owns the field and never re-asserts it.
func TestADeclarativePowerChangeAppliesOnce(t *testing.T) {
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	receiver := testReceiver("theater", fake.address())
	receiver.Spec.Power = equipment.PowerOn
	api.setReceivers(receiver)
	operator := startController(t, api)

	// The first pass starts the driver, which connects asynchronously, so
	// the change waits for a reachable receiver.
	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)

	// Once reachable, the change applies once.
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
}

// A declarative settings change is applied once the receiver is
// reachable, and a re-list with the same settings sends nothing further:
// the operator owns the declared settings and never re-asserts them.
func TestADeclarativeSettingChangeAppliesOnce(t *testing.T) {
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

	mustSucceed(t, operator.pass(t.Context()))
	fake.waitForCommands(t, "PSBAS 53")

	mustSucceed(t, operator.pass(t.Context()))
	fake.refuseCommand(t, "PSBAS 53", quietPeriod)
}

// Declared zone controls reach the right wire lines for each non-main
// zone, and a re-list with the same zones sends nothing further.
func TestDeclaredZoneControlsApplyOnce(t *testing.T) {
	cases := []struct {
		name   string
		zone   string
		power  equipment.Power
		input  string
		volume float64
		mute   bool
		sleep  int
		want   []string
	}{
		{"zone2", "zone2", equipment.PowerOn, "CD", 40, true, 30, []string{"Z2ON", "Z2CD", "Z2MV40", "Z2MUON", "Z2SLP030"}},
		{"zone3", "zone3", equipment.PowerStandby, "TV", 30, false, 30, []string{"Z3OFF", "Z3TV", "Z3MV30", "Z3MUOFF", "Z3SLP030"}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
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

			mustSucceed(t, operator.pass(t.Context()))
			for _, want := range one.want {
				fake.waitForCommands(t, want)
			}

			mustSucceed(t, operator.pass(t.Context()))
			fake.refuseCommand(t, one.want[0], quietPeriod)
		})
	}
}

// A zones map that names the main zone is rejected, so the main zone
// never has two writers: spec.power and spec.zones.
func TestAZonesMapThatNamesMainIsRejected(t *testing.T) {
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	receiver := testReceiver("theater", fake.address())
	receiver.Spec.Zones = map[string]ZoneSpec{"main": {Power: equipment.PowerOn}}
	api.setReceivers(receiver)
	operator := startController(t, api)

	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)
	mustSucceed(t, operator.pass(t.Context()))

	fake.refuseCommand(t, "PWON", quietPeriod)
}

// zonesApplied returns a copy and setZones stores a fresh map, so a
// snapshot read before a later setZones keeps the earlier value; the two
// never share a backing map.
func TestASettledZoneSnapshotSurvivesALaterSetZones(t *testing.T) {
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	receiver := testReceiver("theater", fake.address())
	volume := 30.0
	receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Volume: &volume}}
	api.setReceivers(receiver)
	operator := startController(t, api)

	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)
	mustSucceed(t, operator.pass(t.Context()))
	fake.waitForCommands(t, "Z2MV30")

	unit := operator.units["theater"]
	snapshot := unit.zonesApplied()

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
}

// The unit's own bus opens and subscribes to the settings and commands
// topics with no session standing, so settings and commands reach a
// receiver no Player is using.
func TestTheReceiverBusSubscribesWithoutASession(t *testing.T) {
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	brokers := startFakeBrokerServer(t)
	receiver := testReceiver("theater", fake.address())
	receiver.Spec.SettingsTopic = "liken/equipment/theater/settings"
	receiver.Spec.CommandsTopic = "liken/equipment/theater/commands"
	api.setReceivers(receiver)
	operator := newController(api.client, brokers.address(), testMetrics(t))
	operator.now = func() time.Time { return statusNow }

	mustSucceed(t, operator.pass(t.Context()))

	broker := brokers.waitForSession(t)
	seen := map[string]bool{}
	seen[waitForString(t, broker.subs)] = true
	seen[waitForString(t, broker.subs)] = true
	mustMatch(t, seen[receiver.Spec.SettingsTopic], true)
	mustMatch(t, seen[receiver.Spec.CommandsTopic], true)
}

// A settings bus message sends the value to the receiver and writes the
// same leaf back to spec.denon.settings, in a body that carries only
// that one leaf, so a manifest-declared neighbor is untouched.
func TestASettingsMessageSendsAndWritesTheLeafBack(t *testing.T) {
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	brokers := startFakeBrokerServer(t)
	receiver := testReceiver("theater", fake.address())
	receiver.Spec.SettingsTopic = "liken/equipment/theater/settings"
	api.setReceivers(receiver)
	operator := newController(api.client, brokers.address(), testMetrics(t))
	operator.now = func() time.Time { return statusNow }

	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)
	broker := brokers.waitForSession(t)
	waitForString(t, broker.subs)

	broker.push(receiver.Spec.SettingsTopic, []byte(`{"setting":"tone.bass","value":3}`))
	fake.waitForCommands(t, "PSBAS 53")

	body := <-api.settings
	decoded := map[string]any{}
	mustSucceed(t, json.Unmarshal(body, &decoded))
	spec := decoded["spec"].(map[string]any)
	denonBlock := spec["denon"].(map[string]any)
	settings := denonBlock["settings"].(map[string]any)
	tone := settings["tone"].(map[string]any)
	mustMatch(t, tone["bass"].(float64), float64(3))
	mustMatch(t, len(tone), 1)
	mustMatch(t, len(settings), 1)
	mustMatch(t, len(denonBlock), 1)
	mustMatch(t, len(spec), 1)
}

// A declared setting the receiver cannot carry is logged and not
// recorded, so the next pass tries again; a later valid change lands.
func TestADeclarativeSettingThatFailsToApplyIsRetried(t *testing.T) {
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	receiver := testReceiver("theater", fake.address())
	eco := "turbo"
	receiver.Spec.Denon.Settings = denon.Settings{System: denon.SystemSettings{Eco: &eco}}
	api.setReceivers(receiver)
	operator := startController(t, api)

	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)
	mustSucceed(t, operator.pass(t.Context()))

	// ApplySettings errors on the word no command can carry, so nothing
	// is recorded and the value stays pending.
	bass := 3
	receiver.Spec.Denon.Settings = denon.Settings{Tone: denon.ToneSettings{Bass: &bass}}
	api.setReceivers(receiver)
	mustSucceed(t, operator.pass(t.Context()))

	fake.waitForCommands(t, "PSBAS 53")
}

// A zone control the receiver cannot carry is logged and not recorded,
// so the next pass tries again; a later valid change lands.
func TestAZoneControlThatFailsToApplyIsRetried(t *testing.T) {
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	receiver := testReceiver("theater", fake.address())
	sleep := 200
	receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Sleep: &sleep}}
	api.setReceivers(receiver)
	operator := startController(t, api)

	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)
	mustSucceed(t, operator.pass(t.Context()))

	// SetSleep rejects the timer past the receiver's range, so nothing
	// is recorded and the value stays pending.
	volume := 40.0
	receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Volume: &volume}}
	api.setReceivers(receiver)
	mustSucceed(t, operator.pass(t.Context()))

	fake.waitForCommands(t, "Z2MV40")
}

// A settings message that does not parse, or that names an unknown id,
// changes nothing: the error is logged and no leaf is written.
func TestASettingsMessageThatFailsChangesNothing(t *testing.T) {
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	brokers := startFakeBrokerServer(t)
	receiver := testReceiver("theater", fake.address())
	receiver.Spec.SettingsTopic = "liken/equipment/theater/settings"
	api.setReceivers(receiver)
	operator := newController(api.client, brokers.address(), testMetrics(t))
	operator.now = func() time.Time { return statusNow }

	mustSucceed(t, operator.pass(t.Context()))
	broker := brokers.waitForSession(t)
	waitForString(t, broker.subs)

	broker.push(receiver.Spec.SettingsTopic, []byte(`not json`))
	broker.push(receiver.Spec.SettingsTopic, []byte(`{"setting":"tone.bogus","value":3}`))

	select {
	case body := <-api.settings:
		t.Fatalf("a failing settings message wrote a leaf: %s", body)
	case <-time.After(quietPeriod):
	}
	fake.refuseCommand(t, "PSBAS", quietPeriod)
}

// A slow settings patch on one message does not block the reader, so
// the message behind it on the same topic is still handled. Each
// handler runs on its own goroutine.
func TestASlowSettingsPatchDoesNotBlockTheReader(t *testing.T) {
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	brokers := startFakeBrokerServer(t)
	receiver := testReceiver("theater", fake.address())
	receiver.Spec.SettingsTopic = "liken/equipment/theater/settings"
	api.setReceivers(receiver)
	operator := newController(api.client, brokers.address(), testMetrics(t))
	operator.now = func() time.Time { return statusNow }

	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)
	broker := brokers.waitForSession(t)
	waitForString(t, broker.subs)

	// Hold the API write of the first message open, then push a second.
	// A reader that stalled on the patch would never read the second.
	api.gateSettings()
	defer api.releaseSettings()

	broker.push(receiver.Spec.SettingsTopic, []byte(`{"setting":"tone.bass","value":3}`))
	broker.push(receiver.Spec.SettingsTopic, []byte(`{"setting":"tone.treble","value":1}`))

	// Both handlers run on their own goroutines while the first message's
	// API write is still blocked, so both settings reach the receiver. The
	// two run concurrently, so collect until each one has landed.
	var sawBass, sawTreble bool
	deadline := time.After(testTimeout)
	for !sawBass || !sawTreble {
		select {
		case command := <-fake.commands:
			sawBass = sawBass || command == "PSBAS 53"
			sawTreble = sawTreble || command == "PSTRE 51"
		case <-deadline:
			t.Fatalf("a slow settings patch blocked the reader: bass=%v treble=%v", sawBass, sawTreble)
		}
	}
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

	// The receiver is in standby, so it takes the eco command but keeps
	// reporting the standby value.
	fake.holdSetting("ECOON", "ECOOFF")

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
}

// A declared setting the receiver never reports is sent once and not
// retried: a block with no reported field confirms trivially, so the
// operator stops asking.
func TestASettingTheReceiverNeverReportsSendsOnce(t *testing.T) {
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	receiver := testReceiver("theater", fake.address())
	eco := "on"
	receiver.Spec.Denon.Settings = denon.Settings{System: denon.SystemSettings{Eco: &eco}}
	api.setReceivers(receiver)
	operator := startController(t, api)

	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)

	mustSucceed(t, operator.pass(t.Context()))
	fake.waitForCommands(t, "ECOON")

	mustSucceed(t, operator.pass(t.Context()))
	fake.refuseCommand(t, "ECOON", quietPeriod)
}

// A declared zone control the receiver accepts but does not apply is
// sent again on the next pass until the zone reports the declared
// value, then the operator stops.
func TestAZoneTheReceiverIgnoresIsRetriedUntilReported(t *testing.T) {
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	receiver := testReceiver("theater", fake.address())
	volume := 40.0
	receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Volume: &volume}}
	api.setReceivers(receiver)
	operator := startController(t, api)

	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)

	// The zone takes the volume command but keeps reporting its old
	// volume, so the operator re-sends until the zone reports the
	// declared level. The set command is Z2MV40, and the receiver
	// reports a volume as Z2 followed by the digits.
	fake.holdSetting("Z2MV40", "Z230")

	mustSucceed(t, operator.pass(t.Context()))
	fake.waitForCommands(t, "Z2MV40")
	waitForObservedZone(t, operator, "theater", "zone2", 60)

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
}

// A declared zone control for a zone the receiver never reports is sent
// once and not retried: a zone with no reported state confirms
// trivially, so the operator stops asking.
func TestAZoneTheReceiverNeverReportsSendsOnce(t *testing.T) {
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	receiver := testReceiver("theater", fake.address())
	volume := 40.0
	receiver.Spec.Zones = map[string]ZoneSpec{"zone2": {Volume: &volume}}
	api.setReceivers(receiver)
	operator := startController(t, api)

	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)

	mustSucceed(t, operator.pass(t.Context()))
	fake.waitForCommands(t, "Z2MV40")

	mustSucceed(t, operator.pass(t.Context()))
	fake.refuseCommand(t, "Z2MV40", quietPeriod)
}

// A command bus message reaches Do, and the error Do returns for every
// id is logged and never fatal, so the bus keeps serving later
// messages.
func TestACommandMessageErrorIsLoggedAndNotFatal(t *testing.T) {
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	brokers := startFakeBrokerServer(t)
	receiver := testReceiver("theater", fake.address())
	receiver.Spec.SettingsTopic = "liken/equipment/theater/settings"
	receiver.Spec.CommandsTopic = "liken/equipment/theater/commands"
	api.setReceivers(receiver)
	operator := newController(api.client, brokers.address(), testMetrics(t))
	operator.now = func() time.Time { return statusNow }

	mustSucceed(t, operator.pass(t.Context()))
	api.waitForStatus(t, connected)
	broker := brokers.waitForSession(t)
	waitForString(t, broker.subs)
	waitForString(t, broker.subs)

	// Do errors for every id; the handler logs and the reader moves on,
	// so the settings message that follows still lands. A payload that
	// does not parse changes nothing either.
	broker.push(receiver.Spec.CommandsTopic, []byte(`not json`))
	broker.push(receiver.Spec.CommandsTopic, []byte(`{"command":"quick.3","args":{}}`))
	broker.push(receiver.Spec.SettingsTopic, []byte(`{"setting":"tone.bass","value":3}`))
	fake.waitForCommands(t, "PSBAS 53")
}
