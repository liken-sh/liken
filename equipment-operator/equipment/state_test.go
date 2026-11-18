package equipment

import (
	"encoding/json"
	"testing"
)

func TestZoneReadsOneZoneOutOfAState(t *testing.T) {
	state := State{
		Reachable: ConditionTrue,
		Zones: map[string]ZoneState{
			MainZone: {Power: PowerOn, Volume: 100},
		},
	}

	zone, ok := state.Zone(MainZone)
	if !ok {
		t.Fatal("Zone(MainZone) reports no zone, want the main zone")
	}
	if zone.Power != PowerOn || zone.Volume != 100 {
		t.Errorf("the main zone is %+v, want power %q and volume 100", zone, PowerOn)
	}
}

func TestZoneReportsAZoneTheDriverNeverNamed(t *testing.T) {
	state := State{Zones: map[string]ZoneState{MainZone: {Power: PowerOn}}}

	if _, ok := state.Zone("zone2"); ok {
		t.Error("Zone(\"zone2\") reports a zone, want none because the state names only the main zone")
	}
}

// A driver's State has no zone map before the driver reads anything.
func TestZoneOnAnEmptyState(t *testing.T) {
	var state State

	zone, ok := state.Zone(MainZone)
	if ok {
		t.Errorf("an empty State has the zone %+v, want none", zone)
	}
	if state.Reachable != "" {
		t.Errorf("an empty State has Reachable %q, want empty", state.Reachable)
	}
}

// A stored object can hold a lowercase power.
func TestAPowerReadsAnyCase(t *testing.T) {
	cases := []struct {
		raw  string
		want Power
	}{
		{`"on"`, PowerOn},
		{`"standby"`, PowerStandby},
		{`"off"`, PowerOff},
		{`"On"`, PowerOn},
		{`"STANDBY"`, PowerStandby},
		{`"sleep"`, Power("sleep")},
		{`""`, Power("")},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			var got Power
			if err := json.Unmarshal([]byte(c.raw), &got); err != nil {
				t.Fatalf("unmarshaling %s: %v", c.raw, err)
			}
			if got != c.want {
				t.Errorf("%s reads as %q, want %q", c.raw, got, c.want)
			}
		})
	}
}
