package main

// The descriptions below are cut down from a WiiM Amp and from an
// Arylic amplifier, another brand built on LinkPlay.

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/equipment-operator/wiim"
)

const (
	wiimAmpDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <device>
    <friendlyName>Den Amp</friendlyName>
    <manufacturer>Linkplay Technology Inc.</manufacturer>
    <modelName>WiiM Amp</modelName>
    <modelNumber>V01-Sep 22 2026</modelNumber>
  </device>
</root>`
	arylicDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <device>
    <friendlyName>Porch Amp</friendlyName>
    <manufacturer>Rakoit Technology(SZ) Co., Ltd.</manufacturer>
    <modelName>A50</modelName>
    <modelNumber>V01-Apr 27 2022</modelNumber>
  </device>
</root>`
)

// descriptionServer serves one description with one status code, and
// counts the requests.
type descriptionServer struct {
	url   string
	code  atomic.Int64
	body  atomic.Value
	reads atomic.Int64
}

func startDescription(t *testing.T, code int, body string) *descriptionServer {
	t.Helper()
	served := &descriptionServer{}
	served.code.Store(int64(code))
	served.body.Store(body)
	address := testNetwork.serveHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.reads.Add(1)
		w.WriteHeader(int(served.code.Load()))
		_, _ = w.Write([]byte(served.body.Load().(string)))
	}), nil)
	served.url = "http://" + address + "/description.xml"
	return served
}

// describedDiscovery is a discovery that found the first device, with
// its description at url, and has read the description.
func describedDiscovery(t *testing.T, url string, list ...Receiver) (*discoveryAPI, *discovery, *logBuffer) {
	t.Helper()
	api, client := startDiscoveryAPI(t)
	api.list = receiversWith(list...)
	held := newDiscovery(client, func() {})
	held.dial = testNetwork.dial
	log := &logBuffer{}
	held.log = log
	found := []wiim.Device{{UUID: firstUUID, Address: "192.0.2.1", Description: url}}
	held.store(found)
	held.judge(context.Background(), found)
	return api, held, log
}

func TestDiscoveryCreatesAReceiverForADeviceItsDescriptionNamesAWiiM(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api, held, _ := describedDiscovery(t, startDescription(t, http.StatusOK, wiimAmpDescription).url)

		mustSucceed(t, held.reconcile())

		mustDeepEqual(t, api.appliedNames(), []string{strings.ToLower(firstUUID)})
	})
}

func TestDiscoveryCreatesNothingForADeviceItsDescriptionNamesAnotherModel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api, held, log := describedDiscovery(t, startDescription(t, http.StatusOK, arylicDescription).url)

		mustSucceed(t, held.reconcile())

		mustDeepEqual(t, api.appliedNames(), []string(nil))
		mustDeepEqual(t, log.lines(), []string{
			"discovery skipped the LinkPlay device " + firstUUID + " at 192.0.2.1: its UPnP description names the manufacturer Rakoit Technology(SZ) Co., Ltd. and the model A50, which is not a WiiM",
		})
	})
}

func TestDiscoveryDeletesTheReceiverItMadeForADeviceItsDescriptionNamesAnotherModel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		name := strings.ToLower(firstUUID)
		api, held, log := describedDiscovery(t, startDescription(t, http.StatusOK, arylicDescription).url, discoveredReceiver(name, firstUUID))

		mustSucceed(t, held.reconcile())

		mustDeepEqual(t, api.deletedNames(), []string{name})
		mustMatch(t, log.lines()[1], "deleted the discovered Receiver "+name+": the UPnP description of the device "+firstUUID+" names the model A50, which is not a WiiM")
	})
}

func TestDiscoveryKeepsAPersonsReceiverForADeviceItsDescriptionNamesAnotherModel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api, held, _ := describedDiscovery(t, startDescription(t, http.StatusOK, arylicDescription).url, wiimReceiver("studio", firstUUID))

		mustSucceed(t, held.reconcile())

		mustDeepEqual(t, api.deletedNames(), []string(nil))
	})
}

func TestDiscoveryReadsADescriptionOnceItHasAVerdict(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		served := startDescription(t, http.StatusOK, arylicDescription)
		_, held, log := describedDiscovery(t, served.url)
		found := []wiim.Device{{UUID: firstUUID, Address: "192.0.2.1", Description: served.url}}

		held.judge(context.Background(), found)
		held.judge(context.Background(), found)

		mustMatch(t, served.reads.Load(), int64(1))
		mustMatch(t, len(log.lines()), 1)
	})
}

// When the description cannot be read, discovery creates the Receiver
// and leaves the decision to the getStatusEx check.
func TestDiscoveryCreatesAReceiverWhenTheDescriptionCannotBeRead(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api, held, log := describedDiscovery(t, startDescription(t, http.StatusForbidden, "go away").url)

		mustSucceed(t, held.reconcile())

		mustDeepEqual(t, api.appliedNames(), []string{strings.ToLower(firstUUID)})
		mustMatch(t, strings.Contains(log.lines()[0], "403 Forbidden: go away"), true)
	})
}

func TestDiscoveryCreatesAReceiverForADeviceWithNoDescription(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api, held, log := describedDiscovery(t, "")

		mustSucceed(t, held.reconcile())

		mustDeepEqual(t, api.appliedNames(), []string{strings.ToLower(firstUUID)})
		mustMatch(t, log.lines()[0], "discovery found the WiiM "+firstUUID+" at 192.0.2.1, and no Receiver declares it, so discovery created Receiver "+strings.ToLower(firstUUID))
	})
}

// A failed read is retried on the next search, and the failure is
// logged once.
func TestDiscoveryReadsAFailedDescriptionAgainOnTheNextSearch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		served := startDescription(t, http.StatusServiceUnavailable, "busy")
		name := strings.ToLower(firstUUID)
		api, held, log := describedDiscovery(t, served.url, discoveredReceiver(name, firstUUID))
		found := []wiim.Device{{UUID: firstUUID, Address: "192.0.2.1", Description: served.url}}
		held.judge(context.Background(), found)
		served.code.Store(http.StatusOK)
		served.body.Store(arylicDescription)

		held.judge(context.Background(), found)
		mustSucceed(t, held.reconcile())

		mustMatch(t, served.reads.Load(), int64(3))
		mustDeepEqual(t, api.deletedNames(), []string{name})
		mustMatch(t, len(log.lines()), 3)
	})
}
