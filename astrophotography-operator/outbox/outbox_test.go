package outbox

import (
	"testing"
)

func TestAFramesPlaceInTheOutbox(t *testing.T) {
	cases := []struct {
		name   string
		frame  Frame
		path   string
		dir    string
		prefix string
	}{
		{"a light frame in a directory of the format",
			Frame{Camera: "east-main", Reservation: "east-tonight", Target: "m42", Name: "M42/2026-10-12/Luminance/M42_Luminance_300s_20261012T231506Z_4f2a9c01d3e7"},
			"east-main/east-tonight/m42/M42/2026-10-12/Luminance/M42_Luminance_300s_20261012T231506Z_4f2a9c01d3e7.fits",
			"/outbox/east-main/east-tonight/m42/M42/2026-10-12/Luminance",
			"M42_Luminance_300s_20261012T231506Z_4f2a9c01d3e7"},
		{"a light frame at the top of the target's directory",
			Frame{Camera: "east-main", Reservation: "east-tonight", Target: "m42", Name: "M42_0001"},
			"east-main/east-tonight/m42/M42_0001.fits",
			"/outbox/east-main/east-tonight/m42",
			"M42_0001"},
		{"a centering frame",
			Frame{Camera: "east-main", Reservation: "east-tonight", Name: "0199d3a0-0000-7000-8000-000000000001"},
			"east-main/east-tonight/_centering/0199d3a0-0000-7000-8000-000000000001.fits",
			"/outbox/east-main/east-tonight/_centering",
			"0199d3a0-0000-7000-8000-000000000001"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, prefix := c.frame.Upload()
			if got := c.frame.Path(); got != c.path {
				t.Errorf("Path = %q, want %q", got, c.path)
			}
			if dir != c.dir || prefix != c.prefix {
				t.Errorf("Upload = %q, %q; want %q, %q", dir, prefix, c.dir, c.prefix)
			}
			if parsed, err := Parse(c.path); err != nil || parsed != c.frame {
				t.Errorf("Parse = %+v, %v; want %+v", parsed, err, c.frame)
			}
		})
	}
}

func TestParseRefusesWhatNoFrameIs(t *testing.T) {
	cases := []struct{ name, path string }{
		{"a file of the camera's directory", "east-main/stray.fits"},
		{"a file of the reservation's directory", "east-main/east-tonight/stray.fits"},
		{"a file that is no FITS file", "east-main/east-tonight/m42/M42_0001.xisf"},
		{"an absolute path", "/outbox/east-main/east-tonight/m42/M42_0001.fits"},
		{"an empty part", "east-main//m42/M42_0001.fits"},
		{"a part that leaves the outbox", "east-main/east-tonight/m42/../M42_0001.fits"},
		{"a centering frame in a subdirectory", "east-main/east-tonight/_centering/a/b.fits"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if frame, err := Parse(c.path); err == nil {
				t.Errorf("Parse = %+v, want an error", frame)
			}
		})
	}
}

func TestTheGlobOfATargetsFrames(t *testing.T) {
	if got, want := TargetGlob("m42"), "/outbox/*/*/m42"; got != want {
		t.Errorf("TargetGlob = %q, want %q", got, want)
	}
}
