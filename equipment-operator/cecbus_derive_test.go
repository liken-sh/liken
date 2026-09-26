package main

// The Deployment's derivation for one CECBus: the merged device list
// and the four conditions, from the adapters' entries alone.

import (
	"testing"
	"time"
)

var derivedAt = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

func logical(address int) *int {
	return &address
}

// scannedEntry is an adapter in Control that finished a scan and found
// the given devices.
func scannedEntry(machine string, address int, devices ...CECDevice) CECAdapterStatus {
	return CECAdapterStatus{
		Machine: machine, Mode: CECControl, State: AdapterScanned, PhysicalAddress: "1.3.0.0",
		LogicalAddress: logical(address), OSDName: machine, Devices: devices,
	}
}

var (
	tvDevice       = CECDevice{PhysicalAddress: "0.0.0.0", LogicalAddress: 0, Type: "TV", OSDName: "TV", Power: "Standby"}
	receiverDevice = CECDevice{PhysicalAddress: "1.0.0.0", LogicalAddress: 5, Type: "AudioSystem", OSDName: "AVR", Power: "On"}
)

func busWith(mode CECMode, machines []string, entries ...CECAdapterStatus) *CECBus {
	bus := &CECBus{Metadata: ObjectMeta{Name: "den", Generation: 3}, Spec: CECBusSpec{Mode: mode}}
	for _, machine := range machines {
		bus.Spec.Adapters = append(bus.Spec.Adapters, CECBusAdapter{Machine: machine, Display: "acm-0001-receiver"})
	}
	// Every entry reports at derivedAt unless a case states another
	// time, so only a case about staleness sees a stale entry.
	for index := range entries {
		if entries[index].ReportedAt == "" {
			entries[index].ReportedAt = timestamp(derivedAt)
		}
	}
	bus.Status.Adapters = entries
	return bus
}

// reportedAt sets when an entry last reported.
func reportedAt(entry CECAdapterStatus, at time.Time) CECAdapterStatus {
	entry.ReportedAt = timestamp(at)
	return entry
}

// stopped is an entry whose node workload stopped and released the
// adapter.
func stopped(machine string) CECAdapterStatus {
	return CECAdapterStatus{Machine: machine, Mode: CECControl, State: AdapterStopped, Message: "the node workload received terminated"}
}

// conditionOf finds one condition by type.
func conditionOf(conditions []Condition, kind string) Condition {
	for _, condition := range conditions {
		if condition.Type == kind {
			return condition
		}
	}
	return Condition{}
}

func TestTheConditionsSayWhatTheAdaptersDid(t *testing.T) {
	one := []string{"node-1"}
	two := []string{"node-1", "node-2"}
	cases := []struct {
		name   string
		bus    *CECBus
		kind   string
		status ConditionStatus
		reason string
	}{
		{"an adapter that scanned has an address", busWith(CECControl, one, scannedEntry("node-1", 4, tvDevice)), conditionAddressKnown, ConditionTrue, conditionAddressKnown},
		{"an adapter that scanned joined", busWith(CECControl, one, scannedEntry("node-1", 4, tvDevice)), conditionJoined, ConditionTrue, conditionJoined},
		{"one adapter is coherent by itself", busWith(CECControl, one, scannedEntry("node-1", 4, tvDevice)), conditionCoherent, ConditionTrue, reasonOneAdapter},
		{"an adapter that found the TV scanned", busWith(CECControl, one, scannedEntry("node-1", 4, tvDevice)), conditionScanned, ConditionTrue, conditionScanned},
		{"no entry", busWith(CECControl, one), conditionJoined, ConditionFalse, reasonNotReported},
		{"no entry, no address", busWith(CECControl, one), conditionAddressKnown, ConditionFalse, reasonNotReported},
		{"no entry, no scan", busWith(CECControl, one), conditionScanned, ConditionUnknown, reasonNotReported},
		{"an entry of a machine the spec dropped", busWith(CECControl, []string{"node-2"}, scannedEntry("node-1", 4, tvDevice)), conditionJoined, ConditionFalse, reasonNotReported},
		{"no address from the Display", busWith(CECControl, one, CECAdapterStatus{Machine: "node-1", State: AdapterJoining, Message: "Display acm-0001-receiver has no status.physicalAddress"}),
			conditionAddressKnown, ConditionFalse, reasonNoAddress},
		{"no address and no message", busWith(CECControl, one, CECAdapterStatus{Machine: "node-1", State: AdapterJoining}), conditionAddressKnown, ConditionFalse, reasonNoAddress},
		{"no logical address", busWith(CECControl, one, CECAdapterStatus{Machine: "node-1", State: AdapterJoining, PhysicalAddress: "1.3.0.0"}), conditionJoined, ConditionFalse, reasonNoLogical},
		{"a refused adapter", busWith(CECControl, one, CECAdapterStatus{Machine: "node-1", State: AdapterRefused, Message: "CEC_S_MODE: device or resource busy"}), conditionJoined, ConditionFalse, reasonRefused},
		{"a refused adapter has no scan", busWith(CECControl, one, CECAdapterStatus{Machine: "node-1", State: AdapterRefused, Message: "x"}), conditionScanned, ConditionFalse, reasonRefused},
		{"a scan in progress", busWith(CECControl, one, CECAdapterStatus{Machine: "node-1", State: AdapterJoined, PhysicalAddress: "1.3.0.0", LogicalAddress: logical(4)}), conditionScanned, ConditionUnknown, reasonScanning},
		{"a scan that found nothing", busWith(CECControl, one, scannedEntry("node-1", 4)), conditionScanned, ConditionFalse, reasonNoAnswer},
		{"two adapters that see each other", busWith(CECControl, two,
			scannedEntry("node-1", 4, CECDevice{LogicalAddress: 8, OSDName: "node-2"}),
			scannedEntry("node-2", 8, CECDevice{LogicalAddress: 4, OSDName: "node-1"})), conditionCoherent, ConditionTrue, conditionCoherent},
		{"two adapters on different wires", busWith(CECControl, two,
			scannedEntry("node-1", 4, tvDevice),
			scannedEntry("node-2", 4, tvDevice)), conditionCoherent, ConditionFalse, reasonApart},
		{"an adapter that rejoined at a new address is still seen by name", busWith(CECControl, two,
			scannedEntry("node-1", 4, CECDevice{LogicalAddress: 8, OSDName: "node-2"}),
			scannedEntry("node-2", 11, CECDevice{LogicalAddress: 4, OSDName: "node-1"})), conditionCoherent, ConditionTrue, conditionCoherent},
		{"an adapter that has not scanned is not judged", busWith(CECControl, two,
			scannedEntry("node-1", 4, tvDevice),
			CECAdapterStatus{Machine: "node-2", State: AdapterJoined, PhysicalAddress: "1.4.0.0", LogicalAddress: logical(8), OSDName: "node-2"}), conditionCoherent, ConditionTrue, reasonOneAdapter},
		{"a stale entry has not joined", busWith(CECControl, one, reportedAt(scannedEntry("node-1", 4, tvDevice), derivedAt.Add(-2*time.Minute))), conditionJoined, ConditionUnknown, reasonStale},
		{"a stale entry has no address", busWith(CECControl, one, reportedAt(scannedEntry("node-1", 4, tvDevice), derivedAt.Add(-2*time.Minute))), conditionAddressKnown, ConditionUnknown, reasonStale},
		{"a stale entry has not scanned", busWith(CECControl, one, reportedAt(scannedEntry("node-1", 4, tvDevice), derivedAt.Add(-2*time.Minute))), conditionScanned, ConditionUnknown, reasonStale},
		{"a stale entry sees no one", busWith(CECControl, two,
			scannedEntry("node-1", 4, CECDevice{LogicalAddress: 8, OSDName: "node-2"}),
			reportedAt(scannedEntry("node-2", 8, CECDevice{LogicalAddress: 4, OSDName: "node-1"}), derivedAt.Add(-2*time.Minute))), conditionCoherent, ConditionUnknown, reasonStale},
		{"an entry with no report time is stale", busWith(CECControl, one, reportedAt(scannedEntry("node-1", 4, tvDevice), time.Time{})), conditionJoined, ConditionUnknown, reasonStale},
		{"a stopped adapter has not joined", busWith(CECControl, one, stopped("node-1")), conditionJoined, ConditionFalse, reasonStopped},
		{"a stopped adapter has not scanned", busWith(CECControl, one, stopped("node-1")), conditionScanned, ConditionFalse, reasonStopped},
		{"a recent entry is not stale", busWith(CECControl, one, reportedAt(scannedEntry("node-1", 4, tvDevice), derivedAt.Add(-time.Minute))), conditionJoined, ConditionTrue, conditionJoined},
		{"a bus in Listen joins nothing", busWith(CECListen, one, CECAdapterStatus{Machine: "node-1", State: AdapterListening}), conditionJoined, ConditionFalse, reasonListening},
		{"a bus in Listen announces no address", busWith(CECListen, one, CECAdapterStatus{Machine: "node-1", State: AdapterListening}), conditionAddressKnown, ConditionFalse, reasonListening},
		{"a bus in Listen cannot judge coherence", busWith(CECListen, one), conditionCoherent, ConditionUnknown, reasonListening},
		{"a bus in Listen that heard a device", busWith(CECListen, one, CECAdapterStatus{Machine: "node-1", State: AdapterListening, Devices: []CECDevice{tvDevice}}), conditionScanned, ConditionFalse, reasonListening},
		{"a bus in Listen that heard nothing", busWith(CECListen, one, CECAdapterStatus{Machine: "node-1", State: AdapterListening}), conditionScanned, ConditionFalse, reasonSilent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, conditions := deriveCECBus(c.bus, derivedAt)

			got := conditionOf(conditions, c.kind)
			if got.Status != c.status || got.Reason != c.reason || got.Message == "" || got.ObservedGeneration != 3 {
				t.Errorf("%s = %+v, want %s %s", c.kind, got, c.status, c.reason)
			}
		})
	}
}

// The messages a person reads carry the machine and the source's text.
func TestTheConditionMessagesNameTheAdapterAndTheCause(t *testing.T) {
	cases := []struct {
		name    string
		bus     *CECBus
		kind    string
		message string
	}{
		{"a refusal", busWith(CECControl, []string{"node-1"}, CECAdapterStatus{Machine: "node-1", State: AdapterRefused, Message: "CEC_S_MODE: device or resource busy"}),
			conditionJoined, "the adapter on node-1: CEC_S_MODE: device or resource busy"},
		{"a silent cable", busWith(CECControl, []string{"node-1"}, scannedEntry("node-1", 4)),
			conditionScanned, "no device answered the polls of the adapter on node-1; the cable between the adapter and the receiver may not carry the CEC wire"},
		{"two wires", busWith(CECControl, []string{"node-1", "node-2"}, scannedEntry("node-1", 4, tvDevice), scannedEntry("node-2", 4, tvDevice)),
			conditionCoherent, `the adapter on node-1 does not see the adapter on node-2 by its OSD name "node-2"; the two adapters may be on different wires`},
		{"a stale entry", busWith(CECControl, []string{"node-1"}, reportedAt(scannedEntry("node-1", 4, tvDevice), derivedAt.Add(-2*time.Minute))),
			conditionJoined, "the node workload on node-1 last reported at 2026-09-26T11:58:00Z, more than 1m30s ago; it may have stopped"},
		{"a stopped adapter", busWith(CECControl, []string{"node-1"}, stopped("node-1")),
			conditionJoined, "the node workload on node-1 stopped and released the adapter: the node workload received terminated"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, conditions := deriveCECBus(c.bus, derivedAt)

			mustMatch(t, conditionOf(conditions, c.kind).Message, c.message)
		})
	}
}

func TestAConditionKeepsItsTransitionTimeWhileItHolds(t *testing.T) {
	bus := busWith(CECControl, []string{"node-1"}, scannedEntry("node-1", 4, tvDevice))
	_, first := deriveCECBus(bus, derivedAt)
	bus.Status.Conditions = first

	_, later := deriveCECBus(bus, derivedAt.Add(time.Minute))

	mustDeepEqual(t, later, first)
}

func TestTheDevicesMergeAcrossAdapters(t *testing.T) {
	cases := []struct {
		name    string
		entries []CECAdapterStatus
		want    []CECDevice
	}{
		{"one adapter's devices, in tree order", []CECAdapterStatus{scannedEntry("node-1", 4, receiverDevice, tvDevice)},
			[]CECDevice{tvDevice, receiverDevice}},
		{"one device two adapters see is one device", []CECAdapterStatus{
			scannedEntry("node-1", 4, CECDevice{PhysicalAddress: "0.0.0.0", LogicalAddress: 0, OSDName: "TV"}),
			scannedEntry("node-2", 8, CECDevice{PhysicalAddress: "0.0.0.0", LogicalAddress: 0, Power: "On"}),
		}, []CECDevice{{PhysicalAddress: "0.0.0.0", LogicalAddress: 0, OSDName: "TV", Power: "On"}}},
		{"the first adapter's fact wins", []CECAdapterStatus{
			scannedEntry("node-1", 4, CECDevice{PhysicalAddress: "0.0.0.0", LogicalAddress: 0, Power: "Standby"}),
			scannedEntry("node-2", 8, CECDevice{PhysicalAddress: "0.0.0.0", LogicalAddress: 0, Power: "On"}),
		}, []CECDevice{{PhysicalAddress: "0.0.0.0", LogicalAddress: 0, Power: "Standby"}}},
		{"a heard device with no address joins its logical address", []CECAdapterStatus{
			scannedEntry("node-1", 4, CECDevice{LogicalAddress: 5, Type: "AudioSystem"}),
			scannedEntry("node-2", 8, receiverDevice),
		}, []CECDevice{{PhysicalAddress: "1.0.0.0", LogicalAddress: 5, Type: "AudioSystem", OSDName: "AVR", Power: "On"}}},
		{"a device with no address comes last", []CECAdapterStatus{
			scannedEntry("node-1", 4, CECDevice{LogicalAddress: 1, Type: "Recording"}, receiverDevice),
		}, []CECDevice{receiverDevice, {LogicalAddress: 1, Type: "Recording"}}},
		{"a stale entry's devices are left out", []CECAdapterStatus{
			scannedEntry("node-1", 4, tvDevice),
			reportedAt(scannedEntry("node-2", 8, receiverDevice), derivedAt.Add(-2*time.Minute)),
		}, []CECDevice{tvDevice}},
		{"devices with no address keep logical order after the tree", []CECAdapterStatus{
			scannedEntry("node-1", 4, receiverDevice, CECDevice{LogicalAddress: 9}, tvDevice, CECDevice{LogicalAddress: 1}),
		}, []CECDevice{tvDevice, receiverDevice, {LogicalAddress: 1}, {LogicalAddress: 9}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			machines := []string{}
			for _, entry := range c.entries {
				machines = append(machines, entry.Machine)
			}

			devices, _ := deriveCECBus(busWith(CECControl, machines, c.entries...), derivedAt)

			mustDeepEqual(t, devices, c.want)
		})
	}
}
