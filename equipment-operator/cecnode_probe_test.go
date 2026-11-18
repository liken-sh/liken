package main

// The tests of the node workload's probe, and of how the operator uses
// pod readiness to decide whether another adapter's entry is current.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// probe calls the probe handler once and returns the status code.
func probe(node *cecNode) int {
	recorder := httptest.NewRecorder()
	node.probeHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	return recorder.Code
}

// The fake API server holds an entry write open to block the loop.
func TestTheProbeSucceedsOnlyWhileTheLoopRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := fakeCECBus()
		_, device := usbAdapter(wire)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		node, err := newCECNode(api.client, testNamespace, "node-1", device)
		mustSucceed(t, err)
		runCECNode(t, node)
		api.scanned(t, "node-1")
		mustMatch(t, probe(node), http.StatusOK)
		release := make(chan struct{})
		arrived := make(chan struct{}, 64)
		api.mutex.Lock()
		api.holdingEntries, api.heldEntry = release, arrived
		api.mutex.Unlock()
		wire.Send(cec.ActiveSource(8, 0x1500))
		<-arrived

		mustMatch(t, probe(node), http.StatusServiceUnavailable)

		close(release)
		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.ActiveSource == "1.5.0.0" })
		mustMatch(t, probe(node), http.StatusOK)
	})
}

// With no loop running, the probe fails after exactly cecProbeWait.
func TestTheProbeFailsWithinItsWait(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		_, device := usbAdapter(fakeCECBus())
		node, err := newCECNode(startCECAPI(t).client, testNamespace, "node-1", device)
		mustSucceed(t, err)
		began := time.Now()

		mustMatch(t, probe(node), http.StatusServiceUnavailable)

		mustMatch(t, time.Since(began), cecProbeWait)
	})
}

// The test replaces the package's listen variable, which the metrics
// tests also replace, so it does not run in parallel.
func TestTheProbeListenerServesTheHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kubelet := listenInMemory(t)
		api := startCECAPI(t)
		_, device := usbAdapter(fakeCECBus())
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		node, err := newCECNode(api.client, testNamespace, "node-1", device)
		mustSucceed(t, err)
		runCECNode(t, node)
		api.scanned(t, "node-1")

		address, err := node.serveProbe(t.Context(), "127.0.0.1:0")
		mustSucceed(t, err)
		answer, err := kubelet.Get("http://" + address.String() + "/healthz")
		mustSucceed(t, err)
		defer answer.Body.Close()

		mustMatch(t, answer.StatusCode, http.StatusOK)
	})
}

func TestAnEmptyAddressServesNoProbe(t *testing.T) {
	t.Parallel()
	_, device := usbAdapter(fakeCECBus())
	node, err := newCECNode(startCECAPI(t).client, testNamespace, "node-1", device)
	mustSucceed(t, err)

	address, err := node.serveProbe(t.Context(), "")

	mustSucceed(t, err)
	mustMatch(t, address, nil)
}

// The first adapter of the bus whose pod is Ready sends the bus's
// commands. When that pod stops being Ready, the pod watch wakes the
// next adapter, and it applies spec.power.
func TestTheNextAdapterTakesTheCommandsWhenTheFirstPodIsNotReady(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := busWithTV(televisionTV(cec.PowerStandby))
		_, device := usbAdapter(wire)
		api.putPod("node-1", true)
		api.putPod("node-2", true)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-2", Display: "bnq-0002-monitor"}, CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		first := scannedEntry("node-2", 8)
		mustSucceed(t, ApplyCECAdapterStatus(api.client, "den", "node-2", &first))
		api.putTelevision(lounge(TelevisionOn))
		startNode(t, api, "node-1", device)
		api.scanned(t, "node-1")
		time.Sleep(10 * cecPowerWindow)
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)

		api.putPod("node-2", false)

		appliedAt(t, api, "lounge", 1)
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
	})
}

// While the first adapter's pod is starting and its entry is newer than
// cecStartupGrace, the next adapter sends nothing, so two adapters never
// both send. When the grace ends and the pod is still not Ready, the
// next adapter sends the commands without waiting for a pod event.
func TestTheNextAdapterWaitsForAPodThatIsStarting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := busWithTV(televisionTV(cec.PowerStandby))
		_, device := usbAdapter(wire)
		api.putPod("node-1", true)
		api.putPod("node-2", false)
		time.Sleep(time.Second)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-2", Display: "bnq-0002-monitor"}, CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		first := writtenNow(scannedEntry("node-2", 8))
		mustSucceed(t, ApplyCECAdapterStatus(api.client, "den", "node-2", &first))
		api.putTelevision(lounge(TelevisionOn))
		startNode(t, api, "node-1", device)
		api.scanned(t, "node-1")
		time.Sleep(cecStartupGrace / 2)
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)

		time.Sleep(cecStartupGrace / 2)

		appliedAt(t, api, "lounge", 1)
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
	})
}
