// Package equipment defines Driver, the interface between the receiver
// controller and each protocol driver, such as denon or wiim. The
// controller reads State and sends commands in the units of this
// package, and the driver converts between those units and its
// protocol. A driver imports this package and never the controller.
package equipment

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// ConditionStatus is the status type of kubernetes/conditions, so a
// driver reports reachability in the values that the Receiver's
// conditions show.
type ConditionStatus = conditions.Status

const (
	ConditionTrue    = conditions.True
	ConditionFalse   = conditions.False
	ConditionUnknown = conditions.Unknown
)

// Power is the power state of a zone. The values are in PascalCase,
// the same spelling as the API enum for a Television's power.
type Power string

const (
	PowerOn      Power = "On"
	PowerStandby Power = "Standby"
	PowerOff     Power = "Off"
)

// UnmarshalJSON reads a power in any case and stores the PascalCase
// constant, because a stored object can hold a lowercase power, and the
// API server does not rewrite a stored value when the enum changes. It
// keeps a value that matches no power as it is.
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

// MainZone is the zone that a session controls when it does not name
// another zone. A driver with one zone names it main.
const MainZone = "main"

// ZoneState is what the device last reported for one zone. Volume and
// VolumeMax count the driver's smallest steps, and
// Driver.VolumeResolution gives the number of steps in one display
// unit. A number that the device has not reported is Unknown.
type ZoneState struct {
	Power     Power
	Input     string
	SoundMode string
	Mute      bool
	Volume    int
	VolumeMax int
	// VolumeMaxStable is true when VolumeMax is a fixed maximum, as on
	// a WiiM, whose volume is 0 to 100. A Denon reports a maximum that
	// changes with the volume, so its driver leaves this false.
	VolumeMaxStable bool
	Sleep           int // minutes until standby, or 0 for off
}

// Unknown is the value of a number that the driver has not read.
const Unknown = -1

// State is everything that a driver has read from the device.
type State struct {
	Reachable ConditionStatus
	Zones     map[string]ZoneState
	// Model and Manufacturer are the names that the device reports,
	// such as "WiiM Amp" and "Linkplay Technology Inc.". The operator
	// writes them to status.model and status.manufacturer. They are
	// empty until the driver reads them.
	Model        string
	Manufacturer string
}

// Zone returns the state of one zone, and false when the driver has not
// reported that zone.
func (s State) Zone(name string) (ZoneState, bool) {
	zone, ok := s.Zones[name]
	return zone, ok
}

// Event tells a listener that the driver has read the device. State is
// the whole state after the read. Field is one of the Event constants
// when the read was about that value, and is empty for any other read,
// such as a Denon line for the sleep timer or the end of a WiiM poll.
type Event struct {
	Zone  string
	Field string
	State State
}

// The values of Event.Field.
const (
	EventPower     = "power"
	EventInput     = "input"
	EventVolume    = "volume"
	EventMute      = "mute"
	EventSoundMode = "soundMode"
	EventReachable = "reachable"
	// EventFirstReadDone means FirstReadDone has become true. The Denon driver
	// sends it once. The first full read can finish without a line from the
	// receiver, so no other Event shows that it finished.
	EventFirstReadDone = "firstReadDone"
	// EventModel means the driver has read Model and Manufacturer. The
	// operator uses them only in the status.
	EventModel = "model"
)

// Driver is the interface that each protocol implements. A command is
// sent once and is never sent again after a reconnect, because the
// device may have changed while the connection was down. A driver for a
// device with one zone ignores the zone argument.
type Driver interface {
	// Run keeps the connection to the device open until ctx ends, and
	// reconnects when it drops. It sends an Event to the listener that
	// the driver was built with for each change it reads.
	Run(ctx context.Context)

	// State returns the last state that the driver read. It is safe to
	// call while Run is running.
	State() State

	// Address returns the address that the driver connects to. For a
	// receiver declared by name it is the resolved address, and for a
	// discovered receiver it is the address that discovery found. It
	// appears in the status, so a person can see where the operator
	// connects.
	Address() string

	// FirstReadDone returns whether the driver has finished its first read of
	// the device's settings. The operator applies a declared setting
	// only after that. Before it, a declared setting differs from the
	// empty state, and the operator would send all of the settings.
	FirstReadDone() bool

	// VolumeResolution returns the number of the driver's smallest steps
	// in one display unit. The Denon reports half steps, so it returns 2.
	VolumeResolution() int

	// HasStandby returns whether the device has a standby command. When
	// a powerAsk turns the room off and this is false, the operator
	// sends the device no command, leaves it on, and logs that.
	HasStandby() bool

	// SetPower turns one zone on, or puts it in standby. It returns an
	// error for an unknown zone.
	SetPower(zone string, on bool) error

	// SetInput selects an input on one zone. It returns an error for an
	// unknown zone.
	SetInput(zone, input string) error

	// SetVolume sets the volume of one zone, in the driver's smallest
	// steps. It returns an error for an unknown zone.
	SetVolume(zone string, volume int) error

	// SetMute mutes or unmutes one zone. It returns an error for an
	// unknown zone.
	SetMute(zone string, muted bool) error

	// SetSoundMode selects the sound mode of one zone. It returns an
	// error when the protocol has no sound mode for that zone.
	SetSoundMode(zone, mode string) error

	// SameSoundMode returns whether the reported sound mode is the one
	// that the receiver declaration names. A protocol can report a mode
	// with different text than the command that selects it, so comparing
	// the two strings would send the command again to a receiver that
	// already has the mode.
	SameSoundMode(declared, reported string) bool

	// SetSleep sets the sleep timer of one zone, in minutes. Zero turns
	// it off. It returns an error for an unknown zone or a value out of
	// range.
	SetSleep(zone string, minutes int) error
}
