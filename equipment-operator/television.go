package main

// The Television resource: the TV at the root of one HDMI tree. It
// names its protocol by the block it carries, and the first block is
// cec:, which names the CECBus the TV is on. A bus has at most one TV,
// and the TV is always at logical address 0, so the bus alone
// identifies it. plans/completed/09-cec.md gives the design.

import (
	"slices"
	"strings"
)

// TelevisionPower is the power a person asks of a TV.
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
	// Power is applied once per change and never asserted again, so a
	// person who turns the TV off with its own remote is not overruled.
	Power TelevisionPower `json:"power,omitempty"`
}

// TelevisionSession is a Receiver's session as the TV sees it: the
// Player, the Display the session's input shows, whether the session
// holds the room awake, when it last woke the room, and when a power
// press last turned the room off. The node workload runs one wake for
// each wokeAt, so a wake is a new time and not a change of awake: a
// session wakes the room again while it is awake when a person presses
// the remote's power button, and the TV must wake with the receiver
// then too. It runs one standby for each standbyAt, and only a press
// of the remote's power button writes one, because a TV in a living
// room shows other inputs while the room's player is idle, and a sleep
// of the session must not turn that TV off. It is status and not spec,
// because no person asks for it: the Deployment writes it from the
// Receiver's session, and a status write changes no generation.
type TelevisionSession struct {
	Player    string `json:"player"`
	Display   string `json:"display"`
	Awake     bool   `json:"awake,omitempty"`
	WokeAt    string `json:"wokeAt,omitempty"`
	StandbyAt string `json:"standbyAt,omitempty"`
	// PowerReadAt asks the node workload to read the TV's power once, for
	// a power press that decides from it. status.powerRead answers it.
	PowerReadAt string `json:"powerReadAt,omitempty"`
	// ShowAt asks the node workload to show the session's Display on a TV
	// that is on, for a home press. Each new time is one ask.
	ShowAt string `json:"showAt,omitempty"`
}

// TelevisionPowerRead is the node workload's answer to one
// session.powerReadAt: the request it answers, and the power the TV
// reported then, which is empty when the TV did not answer.
type TelevisionPowerRead struct {
	At    string `json:"at"`
	Power string `json:"power,omitempty"`
}

// TelevisionScreenAsk is what the node workload heard the bus ask of
// the session's Player's screen: Wake for the TV's Set Stream Path to
// the session's Display while the session sleeps, and Sleep for a
// Standby while the session holds the room awake. The Deployment
// relays each new ask to the Player once. Cause names the message in
// words, for the Deployment's line.
type TelevisionScreenAsk struct {
	At     string `json:"at"`
	Player string `json:"player"`
	Screen string `json:"screen"`
	Cause  string `json:"cause,omitempty"`
}

// The two screens a TelevisionScreenAsk asks for.
const (
	screenWake  = "Wake"
	screenSleep = "Sleep"
)

// TelevisionCEC names the CECBus the TV is on.
type TelevisionCEC struct {
	Bus string `json:"bus"`
}

// bus answers the CECBus the Television names, and an empty name for
// a Television with no cec: block.
func (t *Television) bus() string {
	if t.Spec.CEC == nil {
		return ""
	}
	return t.Spec.CEC.Bus
}

// TelevisionStatus has three writers. The Deployment derives cec,
// power, activeSource, activeDisplay, displays, and the Reachable and InCharge
// conditions from the CECBus, the Displays, and the Receivers, and it
// writes session under a field manager of its own. The node workload
// that sends the bus's commands writes powerGeneration and the
// PowerApplied condition when it applies spec.power. The node workload
// that speaks for the session's Display writes wokeAt and the
// WakeApplied condition when it wakes the TV, and standbyAt and the
// StandbyApplied condition when it puts the TV in standby. The node
// workload that sends the bus's commands also writes powerRead when a
// power press asks for the TV's power. The node workload that speaks
// for the session's Display writes screenAsk when the bus asks the
// session's Player's screen to wake or to sleep.
type TelevisionStatus struct {
	CEC   *TelevisionCECStatus `json:"cec,omitempty"`
	Power string               `json:"power,omitempty"`
	// ActiveSource is the physical address of the last Active Source
	// the bus carried.
	ActiveSource string `json:"activeSource,omitempty"`
	// ActiveDisplay is the Display at ActiveSource, or empty when no
	// Display is there.
	ActiveDisplay string `json:"activeDisplay,omitempty"`
	// PowerGeneration is the metadata.generation whose spec.power the
	// node workload applied. Every spec edit is a new generation, so the
	// node workload applies each edit once, and a restart applies none
	// twice.
	PowerGeneration int64 `json:"powerGeneration,omitempty"`
	// Session is the Receiver session that uses this TV.
	Session *TelevisionSession `json:"session,omitempty"`
	// WokeAt is the session.wokeAt whose wake the node workload ran, so
	// a restart runs no wake twice.
	WokeAt string `json:"wokeAt,omitempty"`
	// StandbyAt is the session.standbyAt whose standby the node workload
	// ran, so a restart runs no standby twice.
	StandbyAt string `json:"standbyAt,omitempty"`
	// PowerRead answers the last session.powerReadAt.
	PowerRead *TelevisionPowerRead `json:"powerRead,omitempty"`
	// ScreenAsk is the last ask for the session's Player's screen that
	// the node workload heard on the bus.
	ScreenAsk  *TelevisionScreenAsk `json:"screenAsk,omitempty"`
	Displays   []TelevisionDisplay  `json:"displays,omitempty"`
	Conditions []Condition          `json:"conditions,omitempty"`
}

// TelevisionCECStatus is the TV as the bus's scan found it. A fact the
// TV has not stated is absent.
type TelevisionCECStatus struct {
	PhysicalAddress string `json:"physicalAddress,omitempty"`
	LogicalAddress  int    `json:"logicalAddress"`
	OSDName         string `json:"osdName,omitempty"`
	Vendor          string `json:"vendor,omitempty"`
	CECVersion      string `json:"cecVersion,omitempty"`
}

// TelevisionDisplay is one Display whose picture reaches the TV, and
// the Receiver the picture passes through when one does.
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

// The Television conditions and their reasons. The Deployment writes
// Reachable and InCharge; the node workloads write PowerApplied,
// WakeApplied, and StandbyApplied.
const (
	conditionReachable      = "Reachable"
	conditionInCharge       = "InCharge"
	conditionPowerApplied   = "PowerApplied"
	conditionWakeApplied    = "WakeApplied"
	conditionStandbyApplied = "StandbyApplied"

	reasonInCharge        = "InCharge"
	reasonAnotherInCharge = "AnotherInCharge"

	reasonAnswers         = "Answers"
	reasonNoBus           = "NoBus"
	reasonNotScanned      = "NotScanned"
	reasonNotFound        = "NotFound"
	reasonNoPower         = "NoPowerStatus"
	reasonConfirmed       = "Confirmed"
	reasonUnconfirmed     = "Unconfirmed"
	reasonTaken           = "SourceTaken"
	reasonChosen          = "Chosen"
	reasonTooLate         = "TooLate"
	reasonSuperseded      = "Superseded"
	reasonWaking          = "Waking"
	reasonEnteringStandby = "EnteringStandby"
)

// discovered answers whether discovery owns this Television: it
// carries the discovered label and has its bus's name. A labeled
// Television under another name is a person's, such as a copy of the
// discovered YAML.
func (t *Television) discovered() bool {
	return t.Metadata.Labels[discoveredLabel] != "" && t.Metadata.Name == t.bus()
}

// televisionFor answers the Television that speaks for a bus's TV. A
// person's Television wins over the one the Deployment discovered, and
// of two of a person's, the first by name wins, because a bus has one
// TV.
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
