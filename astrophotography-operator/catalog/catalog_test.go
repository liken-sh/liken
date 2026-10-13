package catalog

// The catalog outlives every version of the operator, so these tests
// fix its wire form: a line that a later version writes differently is
// a change to the format of every archive, and it fails here first.

import (
	"encoding/json"
	"testing"
)

func number(v float64) *float64 { return &v }

func TestTheWireFormOfALine(t *testing.T) {
	cases := []struct {
		name string
		line Line
		want string
	}{
		{"a frame of plan 01's drill",
			Line{
				Record: RecordFrame, FrameID: "0199d3a0-0000-7000-8000-000000000001",
				Target: "m42", Reservation: "east-tonight", Telescope: "east", Camera: "east-main", Train: "east-imaging",
				Path:      "M42/2026-10-12/Luminance/M42_Luminance_10s_20261012T231506Z_000000000001.fits",
				SizeBytes: 2626560, SHA256: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
				StartTime: "2026-10-12T23:15:06.864Z",
				Settings:  Settings{Filter: "Luminance", ExposureSeconds: 10, Binning: 1, Gain: number(100), Offset: number(10)},
			},
			`{"record":"frame","frameID":"0199d3a0-0000-7000-8000-000000000001","target":"m42","reservation":"east-tonight","telescope":"east","camera":"east-main","train":"east-imaging","path":"M42/2026-10-12/Luminance/M42_Luminance_10s_20261012T231506Z_000000000001.fits","sizeBytes":2626560,"sha256":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08","startTime":"2026-10-12T23:15:06.864Z","filter":"Luminance","exposureSeconds":10,"binning":1,"gain":100,"offset":10}`},
		{"a frame with no filter, gain, offset, or start time in its header",
			Line{Record: RecordFrame, FrameID: "f", Target: "t", Reservation: "r", Telescope: "s", Camera: "c",
				Path: "p.fits", SizeBytes: 1, SHA256: "h", Settings: Settings{ExposureSeconds: 0.5, Binning: 2}},
			`{"record":"frame","frameID":"f","target":"t","reservation":"r","telescope":"s","camera":"c","path":"p.fits","sizeBytes":1,"sha256":"h","exposureSeconds":0.5,"binning":2}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := json.Marshal(c.line)
			if err != nil || string(raw) != c.want {
				t.Errorf("the line is\n%s, %v\nwant\n%s", raw, err, c.want)
			}
		})
	}
}

func TestPath(t *testing.T) {
	cases := []struct{ archive, target, want string }{
		{"astro", "m42", "astro/catalog/m42.jsonl"},
		{"", "m42", "catalog/m42.jsonl"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := Path(c.archive, c.target); got != c.want {
				t.Errorf("Path = %q, want %q", got, c.want)
			}
		})
	}
}
