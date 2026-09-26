package main

// What the node workload logs: the adapter's logical addresses when the
// pod opens it, and each change of its entry's state. It logs nothing
// per scan and nothing per steady report.

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
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
	mustMatch(t, lines[1][:len("CECBus den: the adapter on node-1 went from none to ")], "CECBus den: the adapter on node-1 went from none to ")
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
		`CECBus den: the adapter on node-1 went from none to Joining: reading Display acm-0001-receiver: not found`,
	})
}
