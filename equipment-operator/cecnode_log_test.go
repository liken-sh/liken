package main

// What the node workload logs: the adapter's logical addresses when the
// pod opens it, and each change of its entry's state. It logs nothing
// per scan and nothing per steady report.

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// logBuffer collects log lines safely across goroutines.
type logBuffer struct {
	mutex sync.Mutex
	text  bytes.Buffer
}

func (b *logBuffer) Write(line []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.text.Write(line)
}

func (b *logBuffer) lines() []string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return strings.Split(strings.TrimSpace(b.text.String()), "\n")
}

// loggedNode runs a node workload with its log in a buffer until the
// test ends.
func loggedNode(t *testing.T, api *cecAPI, device *cec.Device) *logBuffer {
	t.Helper()
	node, err := newCECNode(api.client, "node-1", device)
	mustSucceed(t, err)
	log := &logBuffer{}
	node.log = log
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = node.run(ctx)
	}()
	t.Cleanup(func() { cancel(); <-stopped })
	return log
}

func TestTheNodeLogsWhatTheAdapterHeldAndEachStateChange(t *testing.T) {
	shorten(t, &cecReportInterval, 10*time.Millisecond)
	api := startCECAPI(t)
	wire := cecRoom()
	adapter, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	// A second handle on the same adapter stands for the previous pod,
	// which left its claim in the kernel.
	previous := cec.New(adapter)
	mustSucceed(t, previous.Follow())
	mustSucceed(t, previous.SetPhysicalAddress(0x1300))
	mustSucceed(t, previous.Claim(cec.Claim{OSDName: "node-1"}))

	log := loggedNode(t, api, device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	settled := len(log.lines())
	before := api.writesOf("node-1")
	api.waitUntil(t, "a few steady reports", func() bool { return api.writesOf("node-1") >= before+3 })

	// The first pass can report Joined before the first scan ends, so
	// the log holds one or two state changes before it settles.
	lines := log.lines()
	mustMatch(t, lines[0], `the adapter on node-1 (cectest) holds logical address 4 as "node-1" when the pod opens it`)
	mustMatch(t, lines[1], "the adapter on node-1 moves from none to CECBus den")
	mustMatch(t, lines[2][:len("CECBus den: the adapter on node-1 went from none to ")], "CECBus den: the adapter on node-1 went from none to ")
	mustMatch(t, lines[len(lines)-1][len(lines[len(lines)-1])-len(" to Scanned"):], " to Scanned")
	mustMatch(t, len(lines), settled)
}

func TestTheNodeLogsAMessageWithTheState(t *testing.T) {
	api := startCECAPI(t)
	_, device := usbAdapter(cecRoom())
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))

	log := loggedNode(t, api, device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterJoining })

	mustDeepEqual(t, log.lines(), []string{
		`the adapter on node-1 (cectest) holds no logical address when the pod opens it`,
		`the adapter on node-1 moves from none to CECBus den`,
		`CECBus den: the adapter on node-1 went from none to Joining: reading Display acm-0001-receiver: not found`,
	})
}

// linesWith answers the log lines that contain a text.
func linesWith(log *logBuffer, text string) []string {
	var found []string
	for _, line := range log.lines() {
		if strings.Contains(line, text) {
			found = append(found, line)
		}
	}
	return found
}

// Two of a person's buses that name one machine are logged once, not
// on every pass, and again only when the set changes.
func TestTwoBusesThatNameOneMachineAreLoggedOnce(t *testing.T) {
	api := startCECAPI(t)
	for _, name := range []string{"study", "den"} {
		api.putBus(listenBus(name))
	}
	_, device := usbAdapter(cecRoom())
	log := loggedNode(t, api, device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })

	for range 5 {
		api.nudge()
	}
	api.putBus(listenBus("attic"))
	api.waitForEntry(t, "attic", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
	for range 5 {
		api.nudge()
	}
	time.Sleep(50 * time.Millisecond)

	mustDeepEqual(t, linesWith(log, "CECBuses name machine"), []string{
		"2 CECBuses name machine node-1: den, study; the adapter follows den, the first by name",
		"3 CECBuses name machine node-1: attic, den, study; the adapter follows attic, the first by name",
	})
}

// A bus rename adds the new bus before it prunes the old one. The
// adapter moves to the new bus, says so, and removes its entry from the
// old bus, which still exists and still names the machine.
func TestAnAdapterThatMovesBusesLeavesTheOldOne(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(listenBus("lounge"))
	_, device := usbAdapter(cecRoom())
	log := loggedNode(t, api, device)
	api.waitForEntry(t, "lounge", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })

	api.putBus(listenBus("living-room"))

	api.waitForEntry(t, "living-room", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
	api.waitUntil(t, "the entry in the old bus to go", func() bool {
		_, held := api.entry("lounge", "node-1")
		return !held
	})
	mustDeepEqual(t, linesWith(log, "moves from"), []string{
		"the adapter on node-1 moves from none to CECBus lounge",
		"the adapter on node-1 moves from CECBus lounge to CECBus living-room",
	})
}

// listenBus is a person's bus in Listen that names node-1.
func listenBus(name string) CECBus {
	return CECBus{Metadata: ObjectMeta{Name: name}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}}
}

// The node workload writes one line when it creates the CECBus of an
// adapter no bus names, and one when a person's bus replaces it.
func TestTheNodeLogsTheBusItCreatesAndDeletes(t *testing.T) {
	api := startCECAPI(t)
	_, device := usbAdapter(cecRoom())
	log := loggedNode(t, api, device)
	api.waitForEntry(t, "node-1", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })

	api.putBus(listenBus("den"))
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
	api.waitUntil(t, "the discovered bus to go", func() bool { return slices.Contains(api.deletedNames(), "node-1") })
	api.nudge()

	mustDeepEqual(t, linesWith(log, "names machine node-1"), []string{
		"no CECBus names machine node-1; created CECBus node-1 in Listen, which sends nothing on the wire",
		"deleted the discovered CECBus node-1: CECBus den names machine node-1",
	})
}

// Each application of a Television's spec.power is one line: what the
// generation asks, what the adapter sent, and what the TV reported.
func TestTheNodeLogsEachApplicationOfThePower(t *testing.T) {
	cases := []struct {
		name string
		tv   cectest.Peer
		want string
	}{
		{
			"a TV that wakes",
			televisionTV(cec.PowerStandby),
			"Television lounge: generation 1 asks On; the adapter on node-1 sent Image View On to the TV once; the TV reported On after <time>",
		},
		{
			"a TV that is on",
			televisionTV(cec.PowerOn),
			"Television lounge: generation 1 asks On; the TV already reported On, so the adapter on node-1 sent no command",
		},
		{
			"a TV that drops each command",
			stubbornTV(),
			"Television lounge: generation 1 asks On; the adapter on node-1 sent Image View On 3 times, and the TV last reported Standby; the application ended after <time>",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			fastPower(t)
			api := startCECAPI(t)
			_, device := usbAdapter(roomWithTV(one.tv))
			api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
			api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
			api.putTelevision(lounge(TelevisionOn))
			log := loggedNode(t, api, device)

			appliedAt(t, api, "lounge", 1)
			api.nudge()
			time.Sleep(2 * cecPowerWindow)

			mustDeepEqual(t, timeless(linesWith(log, "Television lounge")), []string{one.want})
		})
	}
}

// A new generation stops the application of the old one. The old one
// sent a command, so its line says the application stopped.
func TestTheNodeLogsAnApplicationThatANewGenerationStops(t *testing.T) {
	fastPower(t)
	api := startCECAPI(t)
	wire := roomWithTV(stubbornTV())
	_, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	api.putTelevision(lounge(TelevisionOn))
	log := loggedNode(t, api, device)
	api.waitUntil(t, "the first Image View On", func() bool { return sentOf(wire, cec.OpImageViewOn) == 1 })

	api.putTelevision(lounge(TelevisionStandby))

	mustDeepEqual(t, waitForLines(t, log, "generation 1", 1), []string{
		"Television lounge: generation 1 asks On; the adapter on node-1 sent Image View On to the TV, and the application stopped after <time>, before the TV reported On",
	})
}
