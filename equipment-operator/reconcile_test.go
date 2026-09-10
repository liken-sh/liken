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
	client  *Client
	written chan ReceiverStatus
	watched chan string

	mutex     sync.Mutex
	list      ReceiverList
	broken    bool
	statuses  []ReceiverStatus
	refusing  bool
	events    []string
	endStream bool
}

func startFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	api := &fakeAPI{written: make(chan ReceiverStatus, 64), watched: make(chan string, 8)}
	api.client = testAPIClient(t, http.HandlerFunc(api.handle))
	return api
}

func (a *fakeAPI) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/status") {
		a.recordStatus(w, r)
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

func (a *fakeAPI) setReceivers(items ...Receiver) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.list = ReceiverList{Metadata: ListMeta{ResourceVersion: "1"}, Items: items}
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
		return connected(status) && status.SoundMode != ""
	})
	mustMatch(t, status.Power, powerStandby)
	mustMatch(t, status.Input, "MPLAY")
	mustMatch(t, status.Volume, "50")
	mustMatch(t, status.VolumeMax, "69.5")
	mustMatch(t, status.Mute, false)
	mustMatch(t, status.SoundMode, "MULTI CH IN")
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
		return connected(status) && status.SoundMode != ""
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

	api.waitForStatus(t, func(status ReceiverStatus) bool { return status.Volume == "65" })
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

	mustMatch(t, api.waitForStatus(t, connected).Volume, "60")
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
