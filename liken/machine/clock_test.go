package machine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOffsetBoundForEachOffset(t *testing.T) {
	cases := map[time.Duration]string{
		0:                        "±1ms",
		900 * time.Microsecond:   "±1ms",
		time.Millisecond:         "±1ms",
		-3210 * time.Microsecond: "±5ms",
		11 * time.Millisecond:    "±25ms",
		-30 * time.Millisecond:   "±128ms",
		128 * time.Millisecond:   "±128ms",
		200 * time.Millisecond:   "±1s",
		-time.Second:             "±1s",
		1500 * time.Millisecond:  ">1s",
		-90 * time.Second:        ">1s",
	}
	for offset, want := range cases {
		t.Run(offset.String(), func(t *testing.T) {
			if got := OffsetBoundOf(offset).String(); got != want {
				t.Errorf("OffsetBoundOf(%v) = %s, want %s", offset, got, want)
			}
		})
	}
}

func TestOffsetBoundParsesWhatItPrints(t *testing.T) {
	for _, text := range []string{"±1ms", "±5ms", "±25ms", "±128ms", "±1s", ">1s"} {
		t.Run(text, func(t *testing.T) {
			bound, ok := ParseOffsetBound(text)
			if !ok || bound.String() != text {
				t.Errorf("ParseOffsetBound(%q) = %v, %v, want %q and true", text, bound, ok, text)
			}
		})
	}
}

// An exact offset or an empty field is not a bound, so
// ParseOffsetBound rejects it.
func TestOffsetBoundRefusesAnythingElse(t *testing.T) {
	for _, text := range []string{"", "3.21ms", "±2ms", "5ms"} {
		t.Run(text, func(t *testing.T) {
			if _, ok := ParseOffsetBound(text); ok {
				t.Errorf("ParseOffsetBound(%q) returned a bound, want none", text)
			}
		})
	}
}

func TestOffsetBoundOutrunsTheSlewPastTheStepThreshold(t *testing.T) {
	cases := map[string]bool{"±25ms": false, "±128ms": false, "±1s": true, ">1s": true}
	for text, want := range cases {
		t.Run(text, func(t *testing.T) {
			bound, _ := ParseOffsetBound(text)
			if got := bound.OutrunsSlew(); got != want {
				t.Errorf("OutrunsSlew of %s = %v, want %v", text, got, want)
			}
		})
	}
}

func TestClockReadingRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "clock")
	written := ClockReading{
		Offset: -3210 * time.Microsecond,
		Delay:  840 * time.Microsecond,
		Source: "time.cloudflare.com",
		At:     time.Date(2026, 10, 11, 14, 0, 0, 123, time.UTC),
	}
	if err := WriteClockReading(dir, written); err != nil {
		t.Fatalf("writing the clock reading: %v", err)
	}
	read, err := ReadClockReading(dir)
	if err != nil {
		t.Fatalf("reading the clock reading back: %v", err)
	}
	if read != written {
		t.Errorf("ReadClockReading = %+v, want %+v", read, written)
	}
}

// Before the first poll has a reply there is no file, and
// ReadClockReading returns the zero reading.
func TestClockReadingIsZeroBeforeTheFirstPoll(t *testing.T) {
	read, err := ReadClockReading(t.TempDir())
	if err != nil || !read.At.IsZero() {
		t.Errorf("ReadClockReading of an empty directory = %+v, %v, want the zero reading and no error", read, err)
	}
}

func TestClockReadingNamesAnUnparseableFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "reading"), []byte("offset=lots\n"), 0o644); err != nil {
		t.Fatalf("writing the reading file: %v", err)
	}
	if _, err := ReadClockReading(dir); err == nil {
		t.Error("ReadClockReading returned no error for offset=lots, want an error")
	}
}
