// Package equipment is the contract between the receiver controller
// and the protocol driver that reaches one piece of equipment. The
// controller reads a state in its own units and issues commands in
// those units. A driver such as denon translates between the state and
// the wire.
//
// The controller imports this package, and a driver imports this
// package. Neither imports the other, so a new protocol is a new
// directory and a line in the wiring.
package equipment

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// ConditionStatus is the three-valued verdict a condition carries. It
// is the status type of the shared conditions package, which the
// Receiver resource's conditions use, so a driver reports reachability
// in the words the API already holds.
type ConditionStatus = conditions.Status

const (
	ConditionTrue    = conditions.True
	ConditionFalse   = conditions.False
	ConditionUnknown = conditions.Unknown
)

// Power is what a zone reports, in words every protocol shares. The
// words are the Kubernetes enum form, PascalCase, the same as a
// Television's power, so every power the operator reads or writes has
// one spelling.
type Power string

const (
	PowerOn      Power = "On"
	PowerStandby Power = "Standby"
	PowerOff     Power = "Off"
)

// UnmarshalJSON reads a power case-blind into its one form. An older
// operator wrote the lowercase form, and the API server keeps a stored
// value unchanged under a new enum, so an object can still hold it. A
// word that is no power is kept as it is.
func (p *Power) UnmarshalJSON(raw []byte) error {
	var word string
	if err := json.Unmarshal(raw, &word); err != nil {
		return err
	}
	*p = Power(word)
	for _, known := range []Power{PowerOn, PowerStandby, PowerOff} {
		if strings.EqualFold(word, string(known)) {
			*p = known
		}
	}
	return nil
}

// MainZone names the zone a session drives when it names no other. A
// driver that has one zone calls it main.
const MainZone = "main"

// ZoneState is one zone's last reported values. Volume and VolumeMax
// are counts of the driver's smallest step, which VolumeResolution
// turns into display units. A value the equipment has not reported is
// unknownVolume.
type ZoneState struct {
	Power     Power
	Input     string
	SoundMode string
	Mute      bool
	Volume    int
	VolumeMax int
	// VolumeMaxStable says whether VolumeMax is a ceiling a session may
	// map the bus level against. A Denon reports a limit that moves with
	// the volume, so it is false and the ceiling must be declared. A WiiM
	// reports its own fixed 0 to 100 top, so it is true.
	VolumeMaxStable bool
	Sleep           int // minutes until standby, -1 unknown, 0 off
}

// Unknown is the value a driver reports for a number it has not read.
const Unknown = -1

// State is what a driver last read from the equipment.
type State struct {
	Reachable ConditionStatus
	Zones     map[string]ZoneState
	// Model and Manufacturer name the equipment in its maker's words,
	// such as "WiiM Amp" and "Linkplay Technology Inc.", so a person can
	// tell which device a Receiver reaches. A driver that has not read
	// them leaves them empty.
	Model        string
	Manufacturer string
}

// Zone returns one zone's state and whether the driver has reported it.
func (s State) Zone(name string) (ZoneState, bool) {
	zone, held := s.Zones[name]
	return zone, held
}

// Event is one recognized line, the zone and field it named, and the
// whole state after it. A driver sends an event for each field a
// listener cares about, and the listener decides what to do with it.
type Event struct {
	Zone  string
	Field string
	State State
}

// The fields an event can name. A driver reports reachability, the
// zone fields, and the sound mode, and ignores everything else it
// reads.
const (
	EventPower     = "power"
	EventInput     = "input"
	EventVolume    = "volume"
	EventMute      = "mute"
	EventSoundMode = "soundMode"
	EventReachable = "reachable"
	// EventSurveyed says that Surveyed has become true. A driver sends it
	// once, because a survey can complete with no line from the device,
	// and a listener that waits for the survey would otherwise hear
	// nothing.
	EventSurveyed = "surveyed"
	// EventModel says that the driver has read the equipment's model and
	// maker, which reach the status and nothing else.
	EventModel = "model"
)

// Driver is the one interface a protocol implements. Commands are
// one-shot and never replayed, because the equipment may have moved
// while the connection was down. A driver with one zone ignores the
// zone argument.
type Driver interface {
	// Run keeps the connection open until ctx ends, reconnecting as it
	// drops. It reports every recognized line through the listener the
	// driver was built with.
	Run(ctx context.Context)

	// State returns the last reported state and can run concurrently
	// with Run.
	State() State

	// Address answers the address the driver reached the device on. A
	// receiver declared by name reports its resolved address, and a
	// discovered one reports what was found. The status carries it, so a
	// reader sees where the operator reached the device.
	Address() string

	// Surveyed answers whether the driver has completed its first read
	// of the device's own facts. The operator applies a declared setting
	// only after this, because a setting compared against a device that
	// has not reported yet reads as a difference and sends the whole
	// block.
	Surveyed() bool

	// VolumeResolution is the number of the driver's smallest steps in
	// one display unit. The Denon reports half steps, so it answers 2.
	VolumeResolution() int

	// HasStandby answers whether the device has a standby command. A
	// power press that turns the room off reads it: a device with no
	// standby stays on, and the press states that as its outcome and
	// sends the device nothing.
	HasStandby() bool

	// SetPower turns one zone on or to standby. An unknown zone is an
	// error.
	SetPower(zone string, on bool) error

	// SetInput selects one input on one zone. An unknown zone is an
	// error.
	SetInput(zone, input string) error

	// SetVolume sets one zone's volume, in the driver's smallest steps.
	// An unknown zone is an error.
	SetVolume(zone string, volume int) error

	// SetMute sets one zone's mute. An unknown zone is an error.
	SetMute(zone string, muted bool) error

	// SetSoundMode selects one zone's sound mode. A protocol that
	// carries no sound mode on the zone is an error.
	SetSoundMode(zone, mode string) error

	// SameSoundMode answers whether a zone that reports one sound mode
	// runs the mode a person declared. A protocol can report a mode in
	// other words than the command that selects it, and a compare of the
	// two words would send the command again to a receiver that already
	// runs the mode.
	SameSoundMode(declared, reported string) bool

	// SetSleep sets one zone's sleep timer, in minutes, where zero is
	// off. An unknown zone or an out-of-range value is an error.
	SetSleep(zone string, minutes int) error
}
