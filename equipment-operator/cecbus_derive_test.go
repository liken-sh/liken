package main

// The Deployment's derivation for one CECBus: the merged device list
// and the four conditions, from the adapters' entries and the pods of
// the node workload.

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
		Machine: machine, Mode: CECControl, State: AdapterScanned, PhysicalAddress: announced[machine],
		LogicalAddress: logical(address), OSDName: "den", Devices: devices,
	}
}

// announced is the physical address each machine's Display gives its
// adapter. Every adapter on a bus announces the bus's name, so the
// address is what tells two adapters apart.
var announced = map[string]string{"node-1": "1.3.0.0", "node-2": "1.4.0.0"}

// adapterSeen is another adapter of the bus as a scan finds it.
func adapterSeen(machine string, address int) CECDevice {
	return CECDevice{LogicalAddress: address, PhysicalAddress: announced[machine], Type: "Playback", OSDName: "den"}
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
	// Every entry reports at derivedAt, so a message that names the
	// time of the last write is the same in every case.
	for index := range entries {
		if entries[index].ReportedAt == "" {
			entries[index].ReportedAt = timestamp(derivedAt)
		}
	}
	bus.Status.Adapters = entries
	return bus
}

// podsAt is what the pods show at a time, by machine.
func podsAt(now time.Time, machines map[string]podState) podLiveness {
	return podLiveness{machines: machines, now: now}
}

// The states of one machine's pods, against the entries busWith writes
// at derivedAt.
var (
	// ready is a Ready pod.
	ready = podState{ready: true}
	// lost is a pod that stopped being Ready a second after the write.
	lost = podState{since: derivedAt.Add(time.Second)}
	// starting is a pod that has not been Ready since a second before
	// the write, as a new pod is when its first entry lands.
	starting = podState{since: derivedAt.Add(-time.Second)}
)

// allReady is a Ready pod of the node workload on each machine the
// cases name, so only a case about staleness sees a stale entry.
var allReady = podsAt(derivedAt, map[string]podState{"node-1": ready, "node-2": ready})

// stopped is an entry whose node workload stopped and released the
// adapter.
func stopped(machine string) CECAdapterStatus {
	return CECAdapterStatus{Machine: machine, Mode: CECControl, State: AdapterStopped, Message: "the node workload received terminated"}
}

func TestTheConditionsSayWhatTheAdaptersDid(t *testing.T) {
	t.Parallel()
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
			scannedEntry("node-1", 4, adapterSeen("node-2", 8)),
			scannedEntry("node-2", 8, adapterSeen("node-1", 4))), conditionCoherent, ConditionTrue, conditionCoherent},
		{"two adapters on different wires", busWith(CECControl, two,
			scannedEntry("node-1", 4, tvDevice),
			scannedEntry("node-2", 4, tvDevice)), conditionCoherent, ConditionFalse, reasonApart},
		{"an adapter that rejoined at a new logical address is still seen", busWith(CECControl, two,
			scannedEntry("node-1", 4, adapterSeen("node-2", 8)),
			scannedEntry("node-2", 11, adapterSeen("node-1", 4))), conditionCoherent, ConditionTrue, conditionCoherent},
		{"a device with the bus's name at another address is not the adapter", busWith(CECControl, two,
			scannedEntry("node-1", 4, CECDevice{LogicalAddress: 8, PhysicalAddress: "2.0.0.0", OSDName: "den"}),
			scannedEntry("node-2", 8, adapterSeen("node-1", 4))), conditionCoherent, ConditionFalse, reasonApart},
		{"an adapter that has not scanned is not judged", busWith(CECControl, two,
			scannedEntry("node-1", 4, tvDevice),
			CECAdapterStatus{Machine: "node-2", State: AdapterJoined, PhysicalAddress: "1.4.0.0", LogicalAddress: logical(8), OSDName: "den"}), conditionCoherent, ConditionTrue, reasonOneAdapter},
		{"a stopped adapter has not joined", busWith(CECControl, one, stopped("node-1")), conditionJoined, ConditionFalse, reasonStopped},
		{"a stopped adapter has not scanned", busWith(CECControl, one, stopped("node-1")), conditionScanned, ConditionFalse, reasonStopped},
		{"a bus in Listen joins nothing", busWith(CECListen, one, CECAdapterStatus{Machine: "node-1", State: AdapterListening}), conditionJoined, ConditionFalse, reasonListening},
		{"a bus in Listen announces no address", busWith(CECListen, one, CECAdapterStatus{Machine: "node-1", State: AdapterListening}), conditionAddressKnown, ConditionFalse, reasonListening},
		{"a bus in Listen cannot judge coherence", busWith(CECListen, one), conditionCoherent, ConditionUnknown, reasonListening},
		{"a bus in Listen that heard a device", busWith(CECListen, one, CECAdapterStatus{Machine: "node-1", State: AdapterListening, Devices: []CECDevice{tvDevice}}), conditionScanned, ConditionFalse, reasonListening},
		{"a bus in Listen that heard nothing", busWith(CECListen, one, CECAdapterStatus{Machine: "node-1", State: AdapterListening}), conditionScanned, ConditionFalse, reasonSilent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, conditions := deriveCECBus(c.bus, allReady, derivedAt)

			got := conditionOf(conditions, c.kind)
			if got.Status != c.status || got.Reason != c.reason || got.Message == "" || got.ObservedGeneration != 3 {
				t.Errorf("%s = %+v, want %s %s", c.kind, got, c.status, c.reason)
			}
		})
	}
}

// The messages a person reads carry the machine and the source's text.
func TestTheConditionMessagesNameTheAdapterAndTheCause(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		bus     *CECBus
		kind    string
		message string
	}{
		{"a refusal", busWith(CECControl, []string{"node-1"}, CECAdapterStatus{Machine: "node-1", State: AdapterRefused, Message: "CEC_S_MODE: device or resource busy"}),
			conditionJoined, "the adapter on node-1: CEC_S_MODE: device or resource busy"},
		{"a silent cable", busWith(CECControl, []string{"node-1"}, scannedEntry("node-1", 4)),
			conditionScanned, "no device answered the polls of the adapter on node-1; the HDMI cable at the adapter's output may not carry the CEC wire"},
		{"two wires", busWith(CECControl, []string{"node-1", "node-2"}, scannedEntry("node-1", 4, tvDevice), scannedEntry("node-2", 4, tvDevice)),
			conditionCoherent, `the adapter on node-1 does not see the adapter on node-2 at physical address 1.4.0.0; the two adapters may be on different wires`},
		{"a stopped adapter", busWith(CECControl, []string{"node-1"}, stopped("node-1")),
			conditionJoined, "the node workload on node-1 stopped and released the adapter: the node workload received terminated"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, conditions := deriveCECBus(c.bus, allReady, derivedAt)

			mustMatch(t, conditionOf(conditions, c.kind).Message, c.message)
		})
	}
}

func TestAConditionKeepsItsTransitionTimeWhileItHolds(t *testing.T) {
	t.Parallel()
	bus := busWith(CECControl, []string{"node-1"}, scannedEntry("node-1", 4, tvDevice))
	_, first := deriveCECBus(bus, allReady, derivedAt)
	bus.Status.Conditions = first

	_, later := deriveCECBus(bus, allReady, derivedAt.Add(time.Minute))

	mustDeepEqual(t, later, first)
}

func TestTheDevicesMergeAcrossAdapters(t *testing.T) {
	t.Parallel()
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
		// The System Audio Mode carries the time an adapter read or heard
		// it, so the newer report wins whichever adapter made it.
		{"the newer System Audio Mode wins", []CECAdapterStatus{
			scannedEntry("node-1", 4, CECDevice{PhysicalAddress: "1.0.0.0", LogicalAddress: 5, SystemAudioMode: "On", SystemAudioModeAt: "2026-09-26T11:00:00Z"}),
			scannedEntry("node-2", 8, CECDevice{PhysicalAddress: "1.0.0.0", LogicalAddress: 5, SystemAudioMode: "Off", SystemAudioModeAt: "2026-09-26T11:30:00Z"}),
		}, []CECDevice{{PhysicalAddress: "1.0.0.0", LogicalAddress: 5, SystemAudioMode: "Off", SystemAudioModeAt: "2026-09-26T11:30:00Z"}}},
		{"an older System Audio Mode loses", []CECAdapterStatus{
			scannedEntry("node-1", 4, CECDevice{PhysicalAddress: "1.0.0.0", LogicalAddress: 5, SystemAudioMode: "On", SystemAudioModeAt: "2026-09-26T11:30:00Z"}),
			scannedEntry("node-2", 8, CECDevice{PhysicalAddress: "1.0.0.0", LogicalAddress: 5, SystemAudioMode: "Off", SystemAudioModeAt: "2026-09-26T11:00:00Z"}),
		}, []CECDevice{{PhysicalAddress: "1.0.0.0", LogicalAddress: 5, SystemAudioMode: "On", SystemAudioModeAt: "2026-09-26T11:30:00Z"}}},
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

			devices, _ := deriveCECBus(busWith(CECControl, machines, c.entries...), allReady, derivedAt)

			mustDeepEqual(t, devices, c.want)
		})
	}
}

// An entry that is not Stopped describes its adapter while a pod of the
// node workload on its machine is Ready, and while a pod there starts:
// one that has not become Ready since the write, until cecStartupGrace
// after it. A Stopped entry is the node workload's own last word, and
// wins over what the pods show.
func TestAnEntryIsCurrentWhileItsMachinesPodIsReadyOrStarting(t *testing.T) {
	t.Parallel()
	one := []string{"node-1"}
	two := []string{"node-1", "node-2"}
	entry := scannedEntry("node-1", 4, tvDevice)
	lostOne := podsAt(derivedAt, map[string]podState{"node-1": lost, "node-2": ready})
	startingOne := podsAt(derivedAt.Add(cecStartupGrace-time.Second), map[string]podState{"node-1": starting})
	pastGrace := podsAt(derivedAt.Add(cecStartupGrace), map[string]podState{"node-1": starting})
	gone := podsAt(derivedAt, map[string]podState{"node-2": ready})
	cases := []struct {
		name   string
		bus    *CECBus
		live   podLiveness
		kind   string
		status ConditionStatus
		reason string
	}{
		{"a Ready pod's entry joined", busWith(CECControl, one, entry), allReady, conditionJoined, ConditionTrue, conditionJoined},
		{"a pod that lost readiness after the write has not joined", busWith(CECControl, one, entry), lostOne, conditionJoined, ConditionUnknown, reasonStale},
		{"a pod that lost readiness has no address", busWith(CECControl, one, entry), lostOne, conditionAddressKnown, ConditionUnknown, reasonStale},
		{"a pod that lost readiness has not scanned", busWith(CECControl, one, entry), lostOne, conditionScanned, ConditionUnknown, reasonStale},
		{"a starting pod's fresh entry joined", busWith(CECControl, one, entry), startingOne, conditionJoined, ConditionTrue, conditionJoined},
		{"a starting pod past the grace has not joined", busWith(CECControl, one, entry), pastGrace, conditionJoined, ConditionUnknown, reasonStale},
		{"no pod has not joined", busWith(CECControl, one, entry), gone, conditionJoined, ConditionUnknown, reasonStale},
		{"a stale entry sees no one", busWith(CECControl, two,
			scannedEntry("node-1", 4, adapterSeen("node-2", 8)),
			scannedEntry("node-2", 8, adapterSeen("node-1", 4))), lostOne, conditionCoherent, ConditionUnknown, reasonStale},
		{"a Stopped entry wins over a Ready pod", busWith(CECControl, one, stopped("node-1")), allReady, conditionJoined, ConditionFalse, reasonStopped},
		{"a Stopped entry wins over no pod", busWith(CECControl, one, stopped("node-1")), gone, conditionJoined, ConditionFalse, reasonStopped},
		{"unread pods judge no entry", busWith(CECControl, one, entry), podLiveness{}, conditionJoined, ConditionTrue, conditionJoined},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, conditions := deriveCECBus(c.bus, c.live, derivedAt)

			got := conditionOf(conditions, c.kind)
			if got.Status != c.status || got.Reason != c.reason {
				t.Errorf("%s = %+v, want %s %s", c.kind, got, c.status, c.reason)
			}
		})
	}
}

// The stale verdict says what the pods showed.
func TestTheStaleMessageSaysWhatThePodsShow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		live    podLiveness
		message string
	}{
		{"a pod that lost readiness", podsAt(derivedAt, map[string]podState{"node-1": lost}),
			"the equipment-operator-cec pod on machine node-1 stopped being Ready at 2026-09-26T12:00:01Z, after its entry was last written at 2026-09-26T12:00:00Z, so the entry may not describe the adapter"},
		{"a pod past the grace", podsAt(derivedAt.Add(cecStartupGrace), map[string]podState{"node-1": starting}),
			"the equipment-operator-cec pod on machine node-1 has not been Ready in the 1m0s since its entry was written at 2026-09-26T12:00:00Z, so the entry may not describe the adapter"},
		{"no pod", podsAt(derivedAt, map[string]podState{}),
			"no equipment-operator-cec pod runs on machine node-1, so its entry, last written at 2026-09-26T12:00:00Z, may not describe the adapter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, conditions := deriveCECBus(busWith(CECControl, []string{"node-1"}, scannedEntry("node-1", 4, tvDevice)), c.live, derivedAt)

			mustMatch(t, conditionOf(conditions, conditionJoined).Message, c.message)
		})
	}
}

// An entry in a starting pod's grace names the end of the grace, so
// the loop passes again then. Every other entry names no time.
func TestAStartingPodsEntryNamesTheEndOfItsGrace(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		live    podLiveness
		recheck time.Time
	}{
		{"a starting pod", podsAt(derivedAt, map[string]podState{"node-1": starting, "node-2": ready}), derivedAt.Add(cecStartupGrace)},
		{"Ready pods", allReady, time.Time{}},
		{"a pod that lost readiness", podsAt(derivedAt, map[string]podState{"node-1": lost, "node-2": ready}), time.Time{}},
		{"a starting pod past the grace", podsAt(derivedAt.Add(cecStartupGrace), map[string]podState{"node-1": starting, "node-2": ready}), time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bus := busWith(CECControl, []string{"node-1", "node-2"}, scannedEntry("node-1", 4), scannedEntry("node-2", 8))

			mustMatch(t, reportedEntries(bus, c.live).recheck, c.recheck)
		})
	}
}

// A stale entry's devices describe the wire as it was, so the merged
// list leaves them out.
func TestAStaleEntrysDevicesAreLeftOut(t *testing.T) {
	t.Parallel()
	bus := busWith(CECControl, []string{"node-1", "node-2"}, scannedEntry("node-1", 4, tvDevice), scannedEntry("node-2", 8, receiverDevice))

	devices, _ := deriveCECBus(bus, podsAt(derivedAt, map[string]podState{"node-1": ready, "node-2": lost}), derivedAt)

	mustDeepEqual(t, devices, []CECDevice{tvDevice})
}

// One Ready pod on a machine is enough, as during a rollout that runs
// the old pod and the new one there for a moment. Of the pods that are
// not Ready, the latest change of the Ready condition counts.
func TestOneReadyPodOnAMachineIsEnough(t *testing.T) {
	t.Parallel()
	pod := func(node string, ready bool, changed time.Time) cecPod {
		status := "False"
		if ready {
			status = "True"
		}
		var made cecPod
		made.Spec.NodeName = node
		made.Status.Conditions = []podCondition{{Type: "Ready", Status: status, LastTransitionTime: timestamp(changed)}}
		return made
	}

	live := livenessOf([]cecPod{
		pod("node-1", false, derivedAt), pod("node-1", true, derivedAt),
		pod("node-2", false, derivedAt), pod("node-2", false, derivedAt.Add(time.Minute)),
		pod("", true, derivedAt),
	}, derivedAt)

	mustDeepEqual(t, live, podsAt(derivedAt, map[string]podState{
		"node-1": {ready: true, since: derivedAt},
		"node-2": {since: derivedAt.Add(time.Minute)},
	}))
}
