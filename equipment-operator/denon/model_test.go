package denon

import (
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// aiosDescription is a receiver's UPnP description, cut down to the
// fields that the client reads and a few others.
const aiosDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <device>
    <deviceType>urn:schemas-denon-com:device:AiosDevice:1</deviceType>
    <friendlyName>Living Room</friendlyName>
    <manufacturer>Denon</manufacturer>
    <modelName>AVR-X1700H</modelName>
  </device>
</root>`

// describedHarness starts a client against a fake receiver whose
// description server responds with the status code in code. It returns
// the count of description requests.
func describedHarness(t *testing.T, code *atomic.Int64) (*clientHarness, *atomic.Int64) {
	t.Helper()
	reads := &atomic.Int64{}
	server := testNetwork.serveHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		mustMatch(t, r.URL.Path, aiosDescriptionPath)
		w.WriteHeader(int(code.Load()))
		_, _ = w.Write([]byte(aiosDescription))
	}))
	receiver := startFakeReceiver(t)
	harness := startClient(t, receiver.address(), func(client *Client) {
		client.descriptionURL = "http://" + server + aiosDescriptionPath
	})
	harness.receiver = receiver
	return harness, reads
}

func TestTheClientReadsTheModelFromTheReceiversDescription(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		code := &atomic.Int64{}
		code.Store(http.StatusOK)
		harness, reads := describedHarness(t, code)

		state := waitForField(t, harness.events, equipment.EventModel)

		mustMatch(t, state.Model, "AVR-X1700H")
		mustMatch(t, state.Manufacturer, "Denon")
		mustMatch(t, harness.client.State().Model, "AVR-X1700H")
		mustMatch(t, reads.Load(), int64(1))
	})
}

func TestTheClientReadsAFailedDescriptionOnTheNextConnection(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		code := &atomic.Int64{}
		code.Store(http.StatusNotFound)
		harness, reads := describedHarness(t, code)
		waitForField(t, harness.events, equipment.EventFirstReadDone)
		code.Store(http.StatusOK)

		harness.receiver.dropConnections()

		mustMatch(t, waitForField(t, harness.events, equipment.EventModel).Model, "AVR-X1700H")
		mustMatch(t, reads.Load(), int64(2))
	})
}
