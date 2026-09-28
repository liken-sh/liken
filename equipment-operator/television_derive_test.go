package main

// The Deployment's derivation for one Television: the TV's facts, its
// power, the two conditions the Deployment owns, and the Displays that
// reach the TV, from the CECBus, the Displays, and the Receivers
// alone. cecbus_derive_test.go holds the bus fixtures.

import (
	"testing"
)

// derivedBus is a bus in a mode with the device list and the Scanned
// condition the Deployment derived for it.
func derivedBus(mode CECMode, scanned ConditionStatus, devices ...CECDevice) *CECBus {
	bus := busWith(mode, []string{"node-1"})
	bus.Status.Devices = devices
	bus.Status.Conditions = []Condition{{Type: conditionScanned, Status: scanned, Reason: reasonScanning, Message: "the adapter on node-1 has not finished a scan"}}
	return bus
}

func tvOn(bus string) *Television {
	return &Television{Metadata: ObjectMeta{Name: "den", Generation: 2}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: bus}}}
}

// derive derives the Television tvOn makes, which is in charge of its
// bus.
func derive(bus *CECBus, displays []Display, receivers []Receiver) televisionDerived {
	return deriveTelevision(tvOn("den"), "den", bus, displays, receivers, derivedAt)
}

func TestReachableSaysWhetherTheTVAnswers(t *testing.T) {
	t.Parallel()
	silent := tvDevice
	silent.Power = ""
	stale := derivedBus(CECControl, ConditionUnknown)
	stale.Status.Conditions[0].Reason = reasonStale
	stale.Status.Conditions[0].Message = "the node workload on node-1 last reported at 2026-09-26T11:00:00Z, more than 1m30s ago; it may have stopped"
	cases := []struct {
		name    string
		bus     *CECBus
		status  ConditionStatus
		reason  string
		message string
	}{
		{"a TV that answers", derivedBus(CECControl, ConditionTrue, tvDevice), ConditionTrue, reasonAnswers,
			"the TV answers Give Device Power Status on CECBus den"},
		{"a TV that does not answer", derivedBus(CECControl, ConditionTrue, silent), ConditionFalse, reasonNoPower,
			"the TV acknowledges logical address 0 on CECBus den and does not answer Give Device Power Status; a TV in a deep standby or an eco mode can stop answering, and the operator cannot wake it then"},
		{"no TV on a scanned bus", derivedBus(CECControl, ConditionTrue, receiverDevice), ConditionFalse, reasonNotFound,
			"no adapter of CECBus den finds a TV at logical address 0"},
		{"a bus that has not scanned", derivedBus(CECControl, ConditionUnknown), ConditionUnknown, reasonNotScanned,
			"CECBus den has not finished a scan: the adapter on node-1 has not finished a scan"},
		{"a bus whose adapter entry is stale", stale, ConditionUnknown, reasonStale,
			"CECBus den: the node workload on node-1 last reported at 2026-09-26T11:00:00Z, more than 1m30s ago; it may have stopped"},
		{"a bus in Listen", derivedBus(CECListen, ConditionFalse, tvDevice), ConditionUnknown, reasonListening,
			"CECBus den is in Listen, so no adapter asks the TV for its power"},
		{"no bus", nil, ConditionFalse, reasonNoBus, "CECBus den does not exist"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := derive(c.bus, nil, nil).reachable

			if got.Status != c.status || got.Reason != c.reason || got.Message != c.message || got.ObservedGeneration != 2 {
				t.Errorf("got %+v", got)
			}
		})
	}
}

// The TV's facts and power are the TV's entry in the bus's device
// list. A TV that did not answer its power status has no power.
func TestTheTVsFactsComeFromTheBus(t *testing.T) {
	t.Parallel()
	silent := tvDevice
	silent.Power = ""
	cases := []struct {
		name  string
		bus   *CECBus
		cec   *TelevisionCECStatus
		power string
	}{
		{"a TV that answers", derivedBus(CECControl, ConditionTrue, tvDevice, receiverDevice),
			&TelevisionCECStatus{PhysicalAddress: "0.0.0.0", LogicalAddress: 0, OSDName: "TV"}, "Standby"},
		{"a TV that does not answer", derivedBus(CECControl, ConditionTrue, silent),
			&TelevisionCECStatus{PhysicalAddress: "0.0.0.0", LogicalAddress: 0, OSDName: "TV"}, ""},
		{"no TV", derivedBus(CECControl, ConditionTrue, receiverDevice), nil, ""},
		{"no bus", nil, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			derived := derive(c.bus, nil, nil)

			mustDeepEqual(t, derived.cec, c.cec)
			mustMatch(t, derived.power, c.power)
		})
	}
}

// Of two Televisions on one bus, the one not in charge says which one
// is, and that its own spec.power waits.
func TestInChargeNamesTheTelevisionThatSpeaksForTheBus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		chosen  string
		status  ConditionStatus
		reason  string
		message string
	}{
		{"in charge", "den", ConditionTrue, reasonInCharge,
			"this Television speaks for the TV on CECBus den"},
		{"another in charge", "lounge", ConditionFalse, reasonAnotherInCharge,
			"Television lounge speaks for the TV on CECBus den, so the operator applies no spec.power of this Television; when lounge is deleted, this Television speaks for the TV, and the operator applies its spec.power once then"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := deriveTelevision(tvOn("den"), c.chosen, derivedBus(CECControl, ConditionTrue, tvDevice), nil, nil, derivedAt).inCharge

			if got.Status != c.status || got.Reason != c.reason || got.Message != c.message {
				t.Errorf("got %+v", got)
			}
		})
	}
}

// display is a Display on a node at a physical address.
func display(name, node, address string) Display {
	found := Display{Metadata: ObjectMeta{Name: name}}
	found.Status.Node = node
	found.Status.PhysicalAddress = address
	return found
}

// wiredReceiver is a Receiver with one input that a machine's Display
// feeds.
func wiredReceiver(name, machine, monitor string) Receiver {
	return Receiver{Metadata: ObjectMeta{Name: name}, Spec: ReceiverSpec{Inputs: []ReceiverInput{{Name: "MPLAY", Machine: machine, Monitor: monitor}}}}
}

// The Displays are the ones the bus's adapters name, because each
// adapter announces its own Display's physical address on this wire. A
// machine's other output can go to another TV, and a physical address
// does not name its tree, so a Display no adapter names is not listed.
// via names a Receiver only when the bus has an audio system above the
// Display in the tree, so a Receiver input that names a Display
// straight on the TV, such as an optical input, is no path.
func TestTheDisplaysAreTheOnesTheAdaptersName(t *testing.T) {
	t.Parallel()
	control := derivedBus(CECControl, ConditionTrue, tvDevice, receiverDevice)
	control.Spec.Adapters = append(control.Spec.Adapters, CECBusAdapter{Machine: "node-2", Display: "bnq-0002-monitor"})
	noReceiver := derivedBus(CECControl, ConditionTrue, tvDevice)
	listening := derivedBus(CECListen, ConditionFalse, tvDevice)
	cases := []struct {
		name      string
		bus       *CECBus
		displays  []Display
		receivers []Receiver
		want      []TelevisionDisplay
	}{
		{"a Display through a receiver", control,
			[]Display{display("acm-0001-receiver", "node-1", "1.3.0.0")},
			[]Receiver{wiredReceiver("den", "node-1", "acm-0001-receiver")},
			[]TelevisionDisplay{{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0", Via: &EquipmentRef{Kind: "Receiver", Name: "den"}}}},
		{"a Display straight on the TV", control,
			[]Display{display("bnq-0002-monitor", "node-2", "2.0.0.0")}, nil,
			[]TelevisionDisplay{{Name: "bnq-0002-monitor", PhysicalAddress: "2.0.0.0"}}},
		{"a receiver input that names a Display straight on the TV", control,
			[]Display{display("bnq-0002-monitor", "node-2", "2.0.0.0")},
			[]Receiver{wiredReceiver("den", "node-2", "bnq-0002-monitor")},
			[]TelevisionDisplay{{Name: "bnq-0002-monitor", PhysicalAddress: "2.0.0.0"}}},
		{"a receiver input on a bus with no audio system", noReceiver,
			[]Display{display("acm-0001-receiver", "node-1", "1.3.0.0")},
			[]Receiver{wiredReceiver("den", "node-1", "acm-0001-receiver")},
			[]TelevisionDisplay{{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0"}}},
		{"two Displays in tree order", control,
			[]Display{display("bnq-0002-monitor", "node-2", "2.0.0.0"), display("acm-0001-receiver", "node-1", "1.3.0.0")}, nil,
			[]TelevisionDisplay{{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0"}, {Name: "bnq-0002-monitor", PhysicalAddress: "2.0.0.0"}}},
		{"a machine's other output, which no adapter names", control,
			[]Display{display("acm-0001-receiver", "node-1", "1.3.0.0"), display("gsm-0003-study", "node-1", "3.0.0.0")}, nil,
			[]TelevisionDisplay{{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0"}}},
		{"a named Display with no physical address", control,
			[]Display{display("acm-0001-receiver", "node-1", "")}, nil, nil},
		{"a named Display that does not exist", control, nil, nil, nil},
		{"a bus in Listen", listening,
			[]Display{display("acm-0001-receiver", "node-1", "1.3.0.0")}, nil, nil},
		{"a Receiver that names the monitor on another machine", control,
			[]Display{display("acm-0001-receiver", "node-1", "1.3.0.0")},
			[]Receiver{wiredReceiver("study", "node-3", "acm-0001-receiver")},
			[]TelevisionDisplay{{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustDeepEqual(t, derive(c.bus, c.displays, c.receivers).displays, c.want)
		})
	}
}

// Reachable keeps the moment it last changed, so a status that does
// not change is not written again.
func TestReachableKeepsItsTransitionTime(t *testing.T) {
	t.Parallel()
	television := tvOn("den")
	earlier := "2026-09-26T11:00:00Z"
	television.Status.Conditions = []Condition{{Type: conditionReachable, Status: ConditionTrue, LastTransitionTime: earlier}}

	derived := deriveTelevision(television, "den", derivedBus(CECControl, ConditionTrue, tvDevice), nil, nil, derivedAt)

	mustMatch(t, derived.reachable.LastTransitionTime, earlier)
}

// discoveredTV is the Television the Deployment made for a bus: the
// bus's own name and the discovered label.
func discoveredTV(bus string) Television {
	return labeledTV(bus, bus)
}

// labeledTV is a Television with the discovered label under any name,
// such as a copy a person made of the discovered YAML.
func labeledTV(name, bus string) Television {
	return Television{
		Metadata: ObjectMeta{Name: name, Labels: map[string]string{discoveredLabel: discoveredCECLabelValue}},
		Spec:     TelevisionSpec{CEC: &TelevisionCEC{Bus: bus}},
	}
}

func declaredTV(name, bus string) Television {
	return Television{Metadata: ObjectMeta{Name: name}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: bus}}}
}

// Discovery owns only the labeled Television with its bus's name, and
// deletes it only when another Television names the same bus. A person
// adopts the discovered Television by applying their own spec to it,
// and it stays. A labeled Television under another name is a person's.
func TestDiscoveryMakesATelevisionForATVNobodyDeclared(t *testing.T) {
	t.Parallel()
	withTV := []CECBus{*derivedBus(CECControl, ConditionTrue, tvDevice, receiverDevice)}
	listening := []CECBus{*derivedBus(CECListen, ConditionFalse, tvDevice)}
	noTV := []CECBus{*derivedBus(CECControl, ConditionTrue, receiverDevice)}
	adopted := discoveredTV("den")
	adopted.Spec.Power = TelevisionOn
	cases := []struct {
		name        string
		buses       []CECBus
		televisions []Television
		create      []string
		prune       []string
	}{
		{"a TV on a bus in Control", withTV, nil, []string{"den"}, nil},
		{"a TV a person declared", withTV, []Television{declaredTV("lounge", "den")}, nil, nil},
		{"a TV discovered before", withTV, []Television{discoveredTV("den")}, nil, nil},
		{"a person's Television under another name", withTV, []Television{discoveredTV("den"), declaredTV("lounge", "den")}, nil, []string{"den"}},
		{"a person adopts the discovered Television under its name", withTV, []Television{adopted}, nil, nil},
		{"a labeled copy under another name is a person's", withTV, []Television{discoveredTV("den"), labeledTV("lounge", "den")}, nil, []string{"den"}},
		{"a labeled copy under another name alone", withTV, []Television{labeledTV("lounge", "den")}, nil, nil},
		{"a discovered TV whose bus is gone", nil, []Television{discoveredTV("den")}, nil, nil},
		{"a bus in Listen", listening, nil, nil, nil},
		{"a bus with no TV", noTV, nil, nil, nil},
		{"the name belongs to a TV on another bus", withTV, []Television{declaredTV("den", "study")}, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			create, prune := discoverTelevisions(c.buses, c.televisions)

			mustDeepEqual(t, create, c.create)
			mustDeepEqual(t, prune, c.prune)
		})
	}
}

// A person's Television speaks for the bus over the discovered one,
// and of two of a person's, the first by name. A labeled Television
// under a name other than its bus's is a person's.
func TestTheTelevisionForABus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		televisions []Television
		want        string
	}{
		{"the discovered one alone", []Television{discoveredTV("den")}, "den"},
		{"a person's over the discovered one", []Television{discoveredTV("den"), declaredTV("lounge", "den")}, "lounge"},
		{"a labeled copy over the discovered one", []Television{discoveredTV("den"), labeledTV("lounge", "den")}, "lounge"},
		{"the first of a person's two", []Television{declaredTV("beta", "den"), declaredTV("alpha", "den")}, "alpha"},
		{"none on this bus", []Television{declaredTV("study", "study")}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			found := televisionFor(c.televisions, "den")

			name := ""
			if found != nil {
				name = found.Metadata.Name
			}
			mustMatch(t, name, c.want)
		})
	}
}

// The active Display is the Display at the physical address of the
// last Active Source: first a Display the bus's adapters name, then
// another Display on a machine the bus names. A source that is no
// Display, such as a streaming box, names none.
func TestTheActiveDisplayIsTheDisplayAtTheActiveSource(t *testing.T) {
	t.Parallel()
	named := display("acm-0001-receiver", "node-1", "1.3.0.0")
	cases := []struct {
		name     string
		source   string
		displays []Display
		want     string
	}{
		{"a Display an adapter names", "1.3.0.0", []Display{named}, "acm-0001-receiver"},
		{"a streaming box", "1.5.0.0", []Display{named}, ""},
		{"another output of a machine the bus names", "1.4.0.0",
			[]Display{named, display("gsm-0003-study", "node-1", "1.4.0.0")}, "gsm-0003-study"},
		{"a Display on a machine the bus does not name", "1.4.0.0",
			[]Display{named, display("gsm-0003-study", "node-9", "1.4.0.0")}, ""},
		{"a named Display before another at the same address", "1.3.0.0",
			[]Display{display("aaa-0000-first", "node-1", "1.3.0.0"), named}, "acm-0001-receiver"},
		{"no Active Source", "", []Display{display("gsm-0003-study", "node-1", "")}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bus := busWith(CECControl, []string{"node-1"}, CECAdapterStatus{Machine: "node-1", ActiveSource: c.source})

			mustMatch(t, derive(bus, c.displays, nil).activeDisplay, c.want)
		})
	}
}
