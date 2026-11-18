package main

// Other brands of LinkPlay device advertise the same mDNS service as a
// WiiM and respond to the same API, so discovery finds them too.

import (
	"strings"
	"testing"

	"github.com/liken-sh/liken/equipment-operator/wiim"
)

// foreignDiscovery returns a discovery that has found the device with
// firstUUID. Each test then calls skip, as a driver does when the
// device reports another brand's project.
func foreignDiscovery(t *testing.T, list ...Receiver) (*discoveryAPI, *discovery, *logBuffer) {
	t.Helper()
	api, client := startDiscoveryAPI(t)
	api.list = receiversWith(list...)
	held := newDiscovery(client, func() {})
	held.log = &logBuffer{}
	held.store([]wiim.Device{{UUID: firstUUID, Address: "192.0.2.1"}})
	return api, held, held.log.(*logBuffer)
}

func TestDiscoveryCreatesNothingForADeviceThatIsNotAWiiM(t *testing.T) {
	t.Parallel()
	api, held, _ := foreignDiscovery(t)
	held.skip(firstUUID, "ARYLIC_A50TE", "192.0.2.1")

	mustSucceed(t, held.reconcile())

	mustDeepEqual(t, api.appliedNames(), []string(nil))
}

func TestDiscoveryDeletesTheReceiverItMadeForADeviceThatIsNotAWiiM(t *testing.T) {
	t.Parallel()
	name := strings.ToLower(firstUUID)
	api, held, log := foreignDiscovery(t, discoveredReceiver(name, firstUUID))
	held.skip(firstUUID, "ARYLIC_A50TE", "192.0.2.1")

	mustDeepEqual(t, api.deletedNames(), []string{name})
	mustDeepEqual(t, log.lines(), []string{
		"discovery skipped the LinkPlay device " + firstUUID + " at 192.0.2.1: its project is ARYLIC_A50TE, which is not a WiiM",
		"deleted the discovered Receiver " + name + ": the device " + firstUUID + " reports the project ARYLIC_A50TE, which is not a WiiM",
	})
}

func TestDiscoveryKeepsAPersonsReceiverForADeviceThatIsNotAWiiM(t *testing.T) {
	t.Parallel()
	api, held, _ := foreignDiscovery(t, wiimReceiver("studio", firstUUID))
	held.skip(firstUUID, "ARYLIC_A50TE", "192.0.2.1")

	mustDeepEqual(t, api.deletedNames(), []string(nil))
}

func TestDiscoveryLogsADeviceThatIsNotAWiiMOnce(t *testing.T) {
	t.Parallel()
	_, held, log := foreignDiscovery(t)
	for range 3 {
		held.skip(firstUUID, "ARYLIC_A50TE", "192.0.2.1")
		mustSucceed(t, held.reconcile())
	}

	mustDeepEqual(t, log.lines(), []string{
		"discovery skipped the LinkPlay device " + firstUUID + " at 192.0.2.1: its project is ARYLIC_A50TE, which is not a WiiM",
	})
}

func TestUnitTellsDiscoveryWhichDeviceIsNotAWiiM(t *testing.T) {
	t.Parallel()
	var uuid, project, address string
	unit := &receiverUnit{foreign: func(u, p, a string) { uuid, project, address = u, p, a }}
	unit.startDriver(&Receiver{Spec: ReceiverSpec{Wiim: &WiimProtocol{UUID: firstUUID}}}, "192.0.2.1", func(string) {})

	unit.wiimClient.Foreign("ARYLIC_A50TE")

	mustDeepEqual(t, []string{uuid, project, address}, []string{firstUUID, "ARYLIC_A50TE", "192.0.2.1"})
}
