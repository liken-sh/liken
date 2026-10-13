package promote

import (
	"testing"
	"time"
)

func int32p(v int32) *int32 { return &v }

// tonight is a frame of the drill: the Luminance sequence of m42 on
// east, requested at 23:15:06 UTC on 2026-10-12.
func tonight() Values {
	return Values{
		Target: "m42", Object: "M42", Filter: "Luminance", Exposure: 300 * time.Second,
		Binning: int32p(1), Gain: int32p(100), Offset: int32p(10),
		Camera: "east-main", Telescope: "east", Train: "east-imaging", Reservation: "east-tonight",
		Requested: time.Date(2026, 10, 12, 23, 15, 6, 0, time.UTC),
		Number:    37, FrameID: "0199d3a0-0000-7000-8000-4f2a9c01d3e7",
	}
}

func TestNameRendersEachToken(t *testing.T) {
	cases := []struct {
		name   string
		format string
		values func(Values) Values
		want   string
	}{
		{"the default format", "{object}/{night}/{filter}/{object}_{filter}_{exposure}_{datetime}", nil,
			"M42/2026-10-12/Luminance/M42_Luminance_300s_20261012T231506Z_4f2a9c01d3e7"},
		{"the resources", "{target}_{camera}_{telescope}_{train}_{reservation}", nil,
			"m42_east-main_east_east-imaging_east-tonight_4f2a9c01d3e7"},
		{"the camera's settings", "{binning}_{gain}_{offset}", nil, "1x1_100_10_4f2a9c01d3e7"},
		{"settings the sequence leaves to the camera", "{binning}_{gain}_{offset}_{filter}",
			func(v Values) Values { v.Binning, v.Gain, v.Offset, v.Filter = nil, nil, nil, ""; return v },
			"default_default_default_none_4f2a9c01d3e7"},
		{"a number of four digits", "{object}_{n}", nil, "M42_0037_4f2a9c01d3e7"},
		{"a short exposure", "{exposure}", func(v Values) Values { v.Exposure = 500 * time.Millisecond; return v },
			"0.5s_4f2a9c01d3e7"},
		{"a format with {frameid} gets no suffix", "{object}_{frameid}", nil,
			"M42_0199d3a0-0000-7000-8000-4f2a9c01d3e7"},
		{"a value is safe in a path", "{object}/{filter}",
			func(v Values) Values { v.Object, v.Filter = "M 42/Orion", "H-alpha (7nm)"; return v },
			"M_42_Orion/H-alpha__7nm__4f2a9c01d3e7"},
		{"the driver's patterns become lower case", "{object}",
			func(v Values) Values { v.Object = "XXX ISO8601"; return v }, "xxx_iso8601_4f2a9c01d3e7"},
		{"a value cannot climb out of the archive", "{filter}/{object}",
			func(v Values) Values { v.Filter, v.Object = "..", ".hidden"; return v }, "_/_hidden_4f2a9c01d3e7"},
		{"an empty part of the path", "{object}//{n}", nil, "M42/_/0037_4f2a9c01d3e7"},
		{"a token the CRD refuses stays as text", "{object}_{unknown}", nil, "M42__unknown__4f2a9c01d3e7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			values := tonight()
			if c.values != nil {
				values = c.values(values)
			}
			if got := Name(c.format, values); got != c.want {
				t.Errorf("Name = %q, want %q", got, c.want)
			}
		})
	}
}

// One night has one date: the local mean solar date at the
// observatory's longitude, 12 hours before the exposure.
func TestTheNightIsTheDateOfTheEvening(t *testing.T) {
	cases := []struct {
		name      string
		requested time.Time
		longitude float64
		want      string
	}{
		{"Greenwich at 23:00 UTC", time.Date(2026, 10, 12, 23, 0, 0, 0, time.UTC), 0, "2026-10-12"},
		{"Greenwich at 03:00 UTC", time.Date(2026, 10, 13, 3, 0, 0, 0, time.UTC), 0, "2026-10-12"},
		{"Greenwich at noon UTC starts the next night", time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC), 0, "2026-10-13"},
		{"75 degrees west at 04:00 UTC is 23:00 local", time.Date(2026, 10, 13, 4, 0, 0, 0, time.UTC), -75, "2026-10-12"},
		{"75 degrees west at 10:00 UTC is 05:00 local", time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC), -75, "2026-10-12"},
		{"150 degrees east at 12:00 UTC is 22:00 local", time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), 150, "2026-10-12"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			values := tonight()
			values.Requested, values.Longitude = c.requested, c.longitude
			if got := Name("{night}_{frameid}", values); got != c.want+"_"+values.FrameID {
				t.Errorf("Name = %q, want the night %s", got, c.want)
			}
		})
	}
}
