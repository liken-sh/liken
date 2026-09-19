package equipment

import "testing"

func TestZoneReadsOneZoneOutOfAState(t *testing.T) {
	state := State{
		Reachable: ConditionTrue,
		Zones: map[string]ZoneState{
			MainZone: {Power: PowerOn, Volume: 100},
		},
	}

	zone, held := state.Zone(MainZone)
	if !held {
		t.Fatal("the main zone is not held")
	}
	if zone.Power != PowerOn || zone.Volume != 100 {
		t.Errorf("zone = %+v, want power %q and volume 100", zone, PowerOn)
	}
}

func TestZoneReportsAZoneTheDriverNeverNamed(t *testing.T) {
	state := State{Zones: map[string]ZoneState{MainZone: {Power: PowerOn}}}

	if _, held := state.Zone("zone2"); held {
		t.Error("a zone the driver never named reads as held")
	}
}

// A state built with no zone map is the state before a driver has read
// anything, and reading one zone out of it is not a panic.
func TestZoneOnAnEmptyState(t *testing.T) {
	var state State

	zone, held := state.Zone(MainZone)
	if held {
		t.Errorf("an empty state holds %+v", zone)
	}
	if state.Reachable != "" {
		t.Errorf("an empty state reads reachable as %q", state.Reachable)
	}
}
