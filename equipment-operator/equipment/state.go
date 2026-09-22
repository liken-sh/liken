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
)

// ConditionStatus is the three-valued verdict a condition carries. It
// is the same type the Receiver resource uses, so a driver reports
// reachability in the words the API already holds.
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// Power is what a zone reports, in words every protocol shares.
type Power string

const (
	PowerOn      Power = "on"
	PowerStandby Power = "standby"
	PowerOff     Power = "off"
)

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

	// SetSleep sets one zone's sleep timer, in minutes, where zero is
	// off. An unknown zone or an out-of-range value is an error.
	SetSleep(zone string, minutes int) error
}
