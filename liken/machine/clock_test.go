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
				t.Errorf("got %s, want %s", got, want)
			}
		})
	}
}

func TestOffsetBoundParsesWhatItPrints(t *testing.T) {
	for _, text := range []string{"±1ms", "±5ms", "±25ms", "±128ms", "±1s", ">1s"} {
		t.Run(text, func(t *testing.T) {
			bound, ok := ParseOffsetBound(text)
			if !ok || bound.String() != text {
				t.Errorf("got %v %v", bound, ok)
			}
		})
	}
}

// An exact offset from an older release, or an empty field, is not a
// bound, so a reader never mistakes it for one.
func TestOffsetBoundRefusesAnythingElse(t *testing.T) {
	for _, text := range []string{"", "3.21ms", "±2ms", "5ms"} {
		t.Run(text, func(t *testing.T) {
			if _, ok := ParseOffsetBound(text); ok {
				t.Errorf("%q parsed as a bound", text)
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
				t.Errorf("got %v, want %v", got, want)
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
		t.Fatal(err)
	}
	read, err := ReadClockReading(dir)
	if err != nil {
		t.Fatal(err)
	}
	if read != written {
		t.Errorf("got %+v, want %+v", read, written)
	}
}

// Before the first answered poll there is no file, and the reading
// is the zero value, which a reader takes as no reading at all.
func TestClockReadingIsZeroBeforeTheFirstPoll(t *testing.T) {
	read, err := ReadClockReading(t.TempDir())
	if err != nil || !read.At.IsZero() {
		t.Errorf("got %+v, %v", read, err)
	}
}

func TestClockReadingNamesAnUnparseableFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "reading"), []byte("offset=lots\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadClockReading(dir); err == nil {
		t.Error("a bad offset should be an error")
	}
}
