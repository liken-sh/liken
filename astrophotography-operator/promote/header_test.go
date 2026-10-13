package promote

import (
	"bytes"
	"io"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/catalog"
)

func TestACardsValue(t *testing.T) {
	cases := []struct{ field, want string }{
		{"'Luminance'          / filter name", "Luminance"},
		{"'It''s a frame'", "It's a frame"},
		{"'a / in a string'", "a / in a string"},
		{"3.000000E+02 / exposure", "3.000000E+02"},
		{"                   T", "T"},
		{"'unterminated", "unterminated"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := cardValue(c.field); got != c.want {
				t.Errorf("cardValue(%q) = %q, want %q", c.field, got, c.want)
			}
		})
	}
}

func TestTheSettingsOfAHeader(t *testing.T) {
	cases := []struct {
		name  string
		cards []string
		want  catalog.Line
	}{
		{"a header with no binning, gain, offset, or time",
			[]string{card("EXPTIME", "1.5D+01")},
			catalog.Line{Record: catalog.RecordFrame, FrameID: frameID, Settings: catalog.Settings{ExposureSeconds: 15, Binning: 1}}},
		{"a DATE-OBS that is not a time",
			[]string{card("XBINNING", "2"), card("DATE-OBS", "'tonight'")},
			catalog.Line{Record: catalog.RecordFrame, FrameID: frameID, Settings: catalog.Settings{Binning: 2}}},
		{"a DATE-OBS with no fraction",
			[]string{card("DATE-OBS", "'2026-10-12T23:15:06'")},
			catalog.Line{Record: catalog.RecordFrame, FrameID: frameID, StartTime: "2026-10-12T23:15:06.000Z",
				Settings: catalog.Settings{Binning: 1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, err := readHeader(bytes.NewReader(fitsFrame(frameID, c.cards...)))
			if err != nil {
				t.Fatal(err)
			}
			if got := lineOf(h); got.FrameID != c.want.FrameID || got.StartTime != c.want.StartTime || !got.Settings.Equal(c.want.Settings) {
				t.Errorf("lineOf = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestAHeaderThatEndsEarlyIsCut(t *testing.T) {
	if _, err := readHeader(bytes.NewReader(fitsFrame(frameID)[:100])); err != io.ErrUnexpectedEOF {
		t.Errorf("readHeader answered %v", err)
	}
	if _, err := readHeader(bytes.NewReader(nil)); err != io.ErrUnexpectedEOF {
		t.Errorf("readHeader of nothing answered %v", err)
	}
}

func TestTheSizeOfAFrame(t *testing.T) {
	cases := []struct {
		name  string
		cards []string
		want  int64
	}{
		{"no data", []string{card("BITPIX", "8"), card("NAXIS", "0")}, 2880},
		{"a 100 x 100 frame of 32-bit floats", []string{card("BITPIX", "-32"), card("NAXIS", "2"),
			card("NAXIS1", "100"), card("NAXIS2", "100")}, 2880 + 40320},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := header{values: map[string]string{}, size: 2880}
			for _, raw := range c.cards {
				h.values[raw[:8][:len(trimmed(raw[:8]))]] = cardValue(raw[10:])
			}
			if got, err := h.fileSize(); err != nil || got != c.want {
				t.Errorf("fileSize = %d, %v, want %d", got, err, c.want)
			}
		})
	}
	missing := header{values: map[string]string{"BITPIX": "16", "NAXIS": "2", "NAXIS1": "4"}}
	if _, err := missing.fileSize(); err == nil {
		t.Error("a header with no NAXIS2 has a size")
	}
}

func trimmed(s string) string { return string(bytes.TrimSpace([]byte(s))) }
