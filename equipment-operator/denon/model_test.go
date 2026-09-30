// The model the client reads from the receiver's UPnP description.

package denon

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/liken-sh/equipment-operator/equipment"
)

// aiosDescription is the root of a receiver's AIOS description, cut to
// the fields the client reads and the ones around them.
const aiosDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <device>
    <deviceType>urn:schemas-denon-com:device:AiosDevice:1</deviceType>
    <friendlyName>Living Room</friendlyName>
    <manufacturer>Denon</manufacturer>
    <modelName>AVR-X1700H</modelName>
  </device>
</root>`

// describedHarness runs a client against a fake receiver whose
// description answers with code, and counts the reads.
func describedHarness(t *testing.T, code *atomic.Int64) (*clientHarness, *atomic.Int64) {
	t.Helper()
	reads := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		mustMatch(t, r.URL.Path, aiosDescriptionPath)
		w.WriteHeader(int(code.Load()))
		_, _ = w.Write([]byte(aiosDescription))
	}))
	t.Cleanup(server.Close)
	receiver := startFakeReceiver(t)
	harness := startClient(t, receiver.address(), func(client *Client) {
		client.descriptionURL = server.URL + aiosDescriptionPath
	})
	harness.receiver = receiver
	return harness, reads
}

func TestTheClientReadsTheModelFromTheReceiversDescription(t *testing.T) {
	code := &atomic.Int64{}
	code.Store(http.StatusOK)
	harness, reads := describedHarness(t, code)

	state := waitForField(t, harness.events, equipment.EventModel)

	mustMatch(t, state.Model, "AVR-X1700H")
	mustMatch(t, state.Manufacturer, "Denon")
	mustMatch(t, harness.client.State().Model, "AVR-X1700H")
	mustMatch(t, reads.Load(), int64(1))
}

// A description the receiver did not serve is read again on the next
// connection.
func TestTheClientReadsAFailedDescriptionOnTheNextConnection(t *testing.T) {
	shortenBackoff(t)
	code := &atomic.Int64{}
	code.Store(http.StatusNotFound)
	harness, reads := describedHarness(t, code)
	waitForField(t, harness.events, equipment.EventSurveyed)
	code.Store(http.StatusOK)

	harness.receiver.dropConnections()

	mustMatch(t, waitForField(t, harness.events, equipment.EventModel).Model, "AVR-X1700H")
	mustMatch(t, reads.Load(), int64(2))
}
