package main

// Pending for a declared zone: which controls the operator sends, from
// what the zone reports and what the operator applied before.

import (
	"encoding/json"
	"testing"

	"github.com/liken-sh/equipment-operator/equipment"
)

func TestZoneSpecPendingKeepsOnlyTheControlsToSend(t *testing.T) {
	t.Parallel()
	forty, thirty := 40.0, 30.0
	on, off := true, false
	half, hour := 30, 60
	// The zone reports every control: on, CD, 40 (80 half steps),
	// unmuted, and a 30-minute sleep timer.
	held := equipment.ZoneState{Power: equipment.PowerOn, Input: "CD", Volume: 80, Mute: false, Sleep: 30}
	silent := equipment.ZoneState{Volume: equipment.Unknown, Sleep: equipment.Unknown}
	cases := []struct {
		name     string
		want     ZoneSpec
		observed equipment.ZoneState
		reported bool
		previous ZoneSpec
		known    bool
		pending  ZoneSpec
	}{
		{"every control at the declared value",
			ZoneSpec{Power: equipment.PowerOn, Input: "CD", Volume: &forty, Mute: &off, Sleep: &half},
			held, true, ZoneSpec{}, false,
			ZoneSpec{}},
		{"each control at another value",
			ZoneSpec{Power: equipment.PowerStandby, Input: "TV", Volume: &thirty, Mute: &on, Sleep: &hour},
			held, true, ZoneSpec{}, false,
			ZoneSpec{Power: equipment.PowerStandby, Input: "TV", Volume: &thirty, Mute: &on, Sleep: &hour}},
		{"one control at another value",
			ZoneSpec{Input: "CD", Volume: &thirty},
			held, true, ZoneSpec{}, false,
			ZoneSpec{Volume: &thirty}},
		{"a zone the receiver has not reported, after a restart",
			ZoneSpec{Volume: &forty, Mute: &on},
			equipment.ZoneState{}, false, ZoneSpec{}, false,
			ZoneSpec{}},
		{"unreported controls the spec did not change",
			ZoneSpec{Power: equipment.PowerOn, Volume: &forty},
			silent, true, ZoneSpec{Power: equipment.PowerOn, Volume: &forty}, true,
			ZoneSpec{}},
		{"unreported controls the spec changed",
			ZoneSpec{Power: equipment.PowerOn, Input: "CD", Volume: &forty, Sleep: &half},
			silent, true, ZoneSpec{Power: equipment.PowerStandby, Input: "TV", Volume: &thirty, Sleep: &hour}, true,
			ZoneSpec{Power: equipment.PowerOn, Input: "CD", Volume: &forty, Sleep: &half}},
		{"a zone the spec added",
			ZoneSpec{Mute: &on},
			equipment.ZoneState{}, false, ZoneSpec{}, true,
			ZoneSpec{Mute: &on}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := one.want.Pending(one.observed, one.reported, one.previous, one.known, 2)
			mustMatch(t, zoneJSON(t, got), zoneJSON(t, one.pending))
		})
	}
}

// zoneJSON writes a zone the way the log carries it, so two values
// compare by what they hold and not by pointer.
func zoneJSON(t *testing.T, z ZoneSpec) string {
	t.Helper()
	raw, err := json.Marshal(z)
	mustSucceed(t, err)
	return string(raw)
}
