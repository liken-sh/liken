package main

import (
	"encoding/json"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

func TestZoneSpecPendingKeepsOnlyTheControlsToSend(t *testing.T) {
	forty, thirty := 40.0, 30.0
	on, off := true, false
	half, hour := 30, 60
	// The receiver reports volume in half steps, so 80 is volume 40.
	reportsAll := equipment.ZoneState{Power: equipment.PowerOn, Input: "CD", Volume: 80, Mute: false, Sleep: 30}
	reportsNone := equipment.ZoneState{Volume: equipment.Unknown, Sleep: equipment.Unknown}
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
			reportsAll, true, ZoneSpec{}, false,
			ZoneSpec{}},
		{"each control at another value",
			ZoneSpec{Power: equipment.PowerStandby, Input: "TV", Volume: &thirty, Mute: &on, Sleep: &hour},
			reportsAll, true, ZoneSpec{}, false,
			ZoneSpec{Power: equipment.PowerStandby, Input: "TV", Volume: &thirty, Mute: &on, Sleep: &hour}},
		{"one control at another value",
			ZoneSpec{Input: "CD", Volume: &thirty},
			reportsAll, true, ZoneSpec{}, false,
			ZoneSpec{Volume: &thirty}},
		{"a zone the receiver has not reported, after a restart",
			ZoneSpec{Volume: &forty, Mute: &on},
			equipment.ZoneState{}, false, ZoneSpec{}, false,
			ZoneSpec{}},
		{"unreported controls the spec did not change",
			ZoneSpec{Power: equipment.PowerOn, Volume: &forty},
			reportsNone, true, ZoneSpec{Power: equipment.PowerOn, Volume: &forty}, true,
			ZoneSpec{}},
		{"unreported controls the spec changed",
			ZoneSpec{Power: equipment.PowerOn, Input: "CD", Volume: &forty, Sleep: &half},
			reportsNone, true, ZoneSpec{Power: equipment.PowerStandby, Input: "TV", Volume: &thirty, Sleep: &hour}, true,
			ZoneSpec{Power: equipment.PowerOn, Input: "CD", Volume: &forty, Sleep: &half}},
		{"a zone the spec added",
			ZoneSpec{Mute: &on},
			equipment.ZoneState{}, false, ZoneSpec{}, true,
			ZoneSpec{Mute: &on}},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				got := one.want.Pending(one.observed, one.reported, one.previous, one.known, 2)
				mustMatch(t, zoneJSON(t, got), zoneJSON(t, one.pending))
			})
		})
	}
}

// zoneJSON encodes a zone as JSON, so two zones compare by value and
// not by the addresses of their pointer fields.
func zoneJSON(t *testing.T, z ZoneSpec) string {
	t.Helper()
	raw, err := json.Marshal(z)
	mustSucceed(t, err)
	return string(raw)
}
