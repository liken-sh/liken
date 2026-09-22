package main

// The ceiling a session maps the bus level against: a declared
// spec.volume.max wins, and a protocol that reports a stable ceiling of
// its own provides one when nothing is declared.

import (
	"context"
	"testing"

	"github.com/liken-sh/equipment-operator/equipment"
)

// fixedDriver is a driver that reports one state and accepts every
// command, for the tests that only read the state.
type fixedDriver struct {
	state equipment.State
}

func (d *fixedDriver) Run(context.Context)               {}
func (d *fixedDriver) State() equipment.State            { return d.state }
func (d *fixedDriver) Surveyed() bool                    { return true }
func (d *fixedDriver) VolumeResolution() int             { return 1 }
func (d *fixedDriver) SetPower(string, bool) error       { return nil }
func (d *fixedDriver) SetInput(string, string) error     { return nil }
func (d *fixedDriver) SetVolume(string, int) error       { return nil }
func (d *fixedDriver) SetMute(string, bool) error        { return nil }
func (d *fixedDriver) SetSoundMode(string, string) error { return nil }
func (d *fixedDriver) SetSleep(string, int) error        { return nil }

func TestASessionFallsBackToTheReportedCeiling(t *testing.T) {
	reported := &fixedDriver{state: equipment.State{
		Reachable: equipment.ConditionTrue,
		Zones: map[string]equipment.ZoneState{
			equipment.MainZone: {Volume: 50, VolumeMax: 100, VolumeMaxStable: true},
		},
	}}
	quiet := func() ReceiverVolume { return ReceiverVolume{} }

	mustMatch(t, (&session{driver: reported, scale: quiet}).ceiling(), 100)

	// A declared max still wins over the reported one.
	declared := func() ReceiverVolume { return ReceiverVolume{Max: 60} }
	mustMatch(t, (&session{driver: reported, scale: declared}).ceiling(), 60)

	// Neither a declared max nor a stable reported one leaves no
	// ceiling, and the level path waits rather than mapping through zero.
	silent := &fixedDriver{state: equipment.State{Zones: map[string]equipment.ZoneState{}}}
	mustMatch(t, (&session{driver: silent, scale: quiet}).ceiling(), 0)
}
