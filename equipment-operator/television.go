package main

// A Television is the TV at the root of one HDMI tree. spec.cec names
// the CECBus the TV is on. CEC gives the TV logical address 0, so a bus
// has at most one TV, and the bus alone identifies it.

import (
	"slices"
	"strings"
)

// TelevisionPower is the power that a person sets in spec.power.
type TelevisionPower string

const (
	TelevisionOn      TelevisionPower = "On"
	TelevisionStandby TelevisionPower = "Standby"
)

type Television struct {
	APIVersion string           `json:"apiVersion,omitempty"`
	Kind       string           `json:"kind,omitempty"`
	Metadata   ObjectMeta       `json:"metadata"`
	Spec       TelevisionSpec   `json:"spec"`
	Status     TelevisionStatus `json:"status,omitempty"`
}

type TelevisionList struct {
	Metadata ListMeta     `json:"metadata"`
	Items    []Television `json:"items"`
}

type TelevisionSpec struct {
	CEC *TelevisionCEC `json:"cec,omitempty"`
	// Power is applied once for each generation and not applied again
	// afterward, so a person who turns the TV off with its own remote is
	// not overruled.
	Power TelevisionPower `json:"power,omitempty"`
}

// TelevisionSession is the Receiver session that uses this TV: its
// Player, the Display on the session's input, and whether the session
// is awake. The Deployment copies it from the Receiver. It is status
// because no person sets it.
//
// The node workload runs one wake for each new WokeAt and one standby
// for each new StandbyAt. A wake is keyed on a time and not on Awake,
// because a powerAsk that turns the room on while the session is
// already awake must wake the TV again. Only a powerAsk that turns the
// room off writes StandbyAt. A session that goes to sleep leaves the TV on, because the
// TV can be showing another input.
type TelevisionSession struct {
	Player    string `json:"player"`
	Display   string `json:"display"`
	Awake     bool   `json:"awake,omitempty"`
	WokeAt    string `json:"wokeAt,omitempty"`
	StandbyAt string `json:"standbyAt,omitempty"`
	// PowerReadAt requests one read of the TV's power. A powerAsk needs
	// the TV's current power to decide whether the room is on. The node
	// workload writes the result to status.powerRead.
	PowerReadAt string `json:"powerReadAt,omitempty"`
	// ShowAt requests, for a home press, that the node workload switch a
	// TV that is on to the session's Display. Each new time is one
	// request.
	ShowAt string `json:"showAt,omitempty"`
}

// TelevisionPowerRead is the result of one session.powerReadAt. At is
// the request's time, and Power is empty when the TV did not reply.
type TelevisionPowerRead struct {
	At    string `json:"at"`
	Power string `json:"power,omitempty"`
}

// TelevisionScreenAsk is a request, heard on the bus, to wake or sleep
// the session Player's screen. Screen is Wake when the TV sends Set
// Stream Path to the session's Display while the session sleeps, and
// Sleep when a Standby arrives while the session is awake.
// media-operator relays each new request to the Player once, and writes
// Cause, the bus message in words, in its log line.
type TelevisionScreenAsk struct {
	At     string `json:"at"`
	Player string `json:"player"`
	Screen string `json:"screen"`
	Cause  string `json:"cause,omitempty"`
}

// Values of TelevisionScreenAsk.Screen.
const (
	screenWake  = "Wake"
	screenSleep = "Sleep"
)

// TelevisionCEC names the CECBus the TV is on.
type TelevisionCEC struct {
	Bus string `json:"bus"`
}

func (t *Television) bus() string {
	if t.Spec.CEC == nil {
		return ""
	}
	return t.Spec.CEC.Bus
}

// Each field and each condition of TelevisionStatus has one writer.
//
// The Deployment derives cec, power, activeSource, activeDisplay,
// displays, and the Reachable and InCharge conditions from the CECBus,
// the Displays, and the Receivers. It writes session under its own
// field manager.
//
// The node workload of the adapter that sends the bus's commands
// writes powerGeneration and the PowerApplied condition when it applies
// spec.power, and powerRead for each session.powerReadAt.
//
// The node workload of the adapter whose spec.adapters entry names the
// session's Display writes wokeAt and the WakeApplied condition when it
// wakes the TV, standbyAt and the StandbyApplied condition when it puts
// the TV in standby, and screenAsk.
type TelevisionStatus struct {
	CEC   *TelevisionCECStatus `json:"cec,omitempty"`
	Power string               `json:"power,omitempty"`
	// ActiveSource is the physical address from the last Active Source
	// message on the bus.
	ActiveSource string `json:"activeSource,omitempty"`
	// ActiveDisplay is the Display at ActiveSource, or empty when no
	// Display has that address.
	ActiveDisplay string `json:"activeDisplay,omitempty"`
	// PowerGeneration is the metadata.generation whose spec.power the
	// node workload applied. Every spec edit makes a new generation, so
	// the node workload applies each edit once, and a restart does not
	// apply one again.
	PowerGeneration int64 `json:"powerGeneration,omitempty"`
	// Session is the Receiver session that uses this TV.
	Session *TelevisionSession `json:"session,omitempty"`
	// WokeAt is the session.wokeAt whose wake the node workload ran, so a
	// restart does not run a wake again.
	WokeAt string `json:"wokeAt,omitempty"`
	// StandbyAt is the session.standbyAt whose standby the node workload
	// ran, so a restart does not run a standby again.
	StandbyAt string `json:"standbyAt,omitempty"`
	// PowerRead is the result of the last session.powerReadAt.
	PowerRead  *TelevisionPowerRead `json:"powerRead,omitempty"`
	ScreenAsk  *TelevisionScreenAsk `json:"screenAsk,omitempty"`
	Displays   []TelevisionDisplay  `json:"displays,omitempty"`
	Conditions []Condition          `json:"conditions,omitempty"`
}

// TelevisionCECStatus is the TV as the bus's scan found it. A field
// that the TV did not report is absent.
type TelevisionCECStatus struct {
	PhysicalAddress string `json:"physicalAddress,omitempty"`
	LogicalAddress  int    `json:"logicalAddress"`
	OSDName         string `json:"osdName,omitempty"`
	Vendor          string `json:"vendor,omitempty"`
	CECVersion      string `json:"cecVersion,omitempty"`
}

// TelevisionDisplay is one Display whose picture reaches the TV, and
// the Receiver that the picture passes through, when there is one.
type TelevisionDisplay struct {
	Name            string        `json:"name"`
	PhysicalAddress string        `json:"physicalAddress"`
	Via             *EquipmentRef `json:"via,omitempty"`
}

// EquipmentRef names one object of this operator's group.
type EquipmentRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// discovered returns whether discovery created this Television: it has
// the discovered label and the same name as its bus. A labeled
// Television with another name belongs to a person, for example a copy
// of the discovered YAML.
func (t *Television) discovered() bool {
	return t.Metadata.Labels[discoveredLabel] != "" && t.Metadata.Name == t.bus()
}

// televisionFor returns the Television of a bus's TV. A bus has one TV,
// so when several Televisions name the bus, a declared one wins over a
// discovered one, and then the first by name wins.
func televisionFor(list []Television, bus string) *Television {
	var declared, discovered []*Television
	for index := range list {
		television := &list[index]
		if television.bus() != bus {
			continue
		}
		if television.discovered() {
			discovered = append(discovered, television)
		} else {
			declared = append(declared, television)
		}
	}
	for _, found := range [][]*Television{declared, discovered} {
		if len(found) > 0 {
			return slices.MinFunc(found, func(a, b *Television) int {
				return strings.Compare(a.Metadata.Name, b.Metadata.Name)
			})
		}
	}
	return nil
}
