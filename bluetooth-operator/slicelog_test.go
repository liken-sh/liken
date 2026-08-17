package main

// These tests cover the lines the operator prints about its slice: one
// line for every write that names what moved, and a rate-limited line
// for the passes that write nothing.

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// unusableTaint is this operator's NoSchedule taint, the one nothing
// tolerates. Its key differs from operator to operator. What it
// exercises here does not.
const unusableTaint = noInputNodeTaint

// sliceLogCapture replaces the operator's reporter with one that
// writes to a buffer and reads a clock the test moves.
type sliceLogCapture struct {
	out   bytes.Buffer
	clock time.Time
}

func captureSliceLog(t *testing.T) *sliceLogCapture {
	t.Helper()
	capture := &sliceLogCapture{clock: time.Date(2026, 8, 16, 2, 0, 0, 0, time.UTC)}
	previous := sliceLog
	sliceLog = &sliceReport{
		out: &capture.out,
		now: func() time.Time { return capture.clock },
	}
	t.Cleanup(func() { sliceLog = previous })
	return capture
}

func (c *sliceLogCapture) advance(d time.Duration) { c.clock = c.clock.Add(d) }

func (c *sliceLogCapture) lines() []string {
	text := strings.TrimSuffix(c.out.String(), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// only reads the one line the test expects, and fails on any other
// count.
func (c *sliceLogCapture) only(t *testing.T) string {
	t.Helper()
	lines := c.lines()
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %q", len(lines), lines)
	}
	return lines[0]
}

// loggedDevice is one device as the slice carries it, with the taint
// keys the case needs.
func loggedDevice(name string, keys ...string) SliceDevice {
	device := SliceDevice{
		Name:       name,
		Attributes: map[string]DeviceAttribute{"where": AttrString(name)},
	}
	for _, key := range keys {
		device.Taints = append(device.Taints, DeviceTaint{Key: key, Effect: "NoSchedule"})
	}
	return device
}

// relabelled copies a device with a different attribute value, which
// is the one change that moves no taint.
func relabelled(device SliceDevice, where string) SliceDevice {
	device.Attributes = map[string]DeviceAttribute{"where": AttrString(where)}
	return device
}

func TestSliceLogNamesWhatAWriteChanged(t *testing.T) {
	cases := []struct {
		name      string
		published []SliceDevice
		current   []SliceDevice
		want      string
	}{
		{
			name:      "a device gained a taint",
			published: []SliceDevice{loggedDevice("device-a"), loggedDevice("device-b")},
			current:   []SliceDevice{loggedDevice("device-a"), loggedDevice("device-b", disconnectedTaint)},
			want:      "slice: wrote generation 5, 2 devices, 1 tainted: device-b gained " + disconnectedTaint,
		},
		{
			name:      "a device lost both taints",
			published: []SliceDevice{loggedDevice("device-a", disconnectedTaint, unusableTaint), loggedDevice("device-b")},
			current:   []SliceDevice{loggedDevice("device-a"), loggedDevice("device-b")},
			want: "slice: wrote generation 5, 2 devices, 0 tainted: device-a lost " +
				disconnectedTaint + ", " + unusableTaint,
		},
		{
			name:      "a device gained one taint and lost another",
			published: []SliceDevice{loggedDevice("device-a", disconnectedTaint)},
			current:   []SliceDevice{loggedDevice("device-a", unusableTaint)},
			want: "slice: wrote generation 5, 1 device, 1 tainted: device-a gained " +
				unusableTaint + "; device-a lost " + disconnectedTaint,
		},
		{
			name:      "a device appeared",
			published: []SliceDevice{loggedDevice("device-a")},
			current:   []SliceDevice{loggedDevice("device-a"), loggedDevice("device-b")},
			want:      "slice: wrote generation 5, 2 devices, 0 tainted: device-b appeared",
		},
		{
			name:      "a device appeared already tainted",
			published: []SliceDevice{loggedDevice("device-a")},
			current:   []SliceDevice{loggedDevice("device-a"), loggedDevice("device-b", unusableTaint)},
			want:      "slice: wrote generation 5, 2 devices, 1 tainted: device-b appeared with " + unusableTaint,
		},
		{
			name:      "a device left",
			published: []SliceDevice{loggedDevice("device-a"), loggedDevice("device-b")},
			current:   []SliceDevice{loggedDevice("device-a")},
			want:      "slice: wrote generation 5, 1 device, 0 tainted: device-b left",
		},
		{
			name:      "only the attributes changed",
			published: []SliceDevice{loggedDevice("device-a")},
			current:   []SliceDevice{relabelled(loggedDevice("device-a"), "somewhere else")},
			want:      "slice: wrote generation 5, 1 device, 0 tainted: device-a changed attributes",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			capture := captureSliceLog(t)
			sliceLog.wrote(5, c.published, c.current)
			if got := capture.only(t); got != c.want {
				t.Errorf("line = %q, want %q", got, c.want)
			}
		})
	}
}

// A create names the devices that arrive unable to serve. Every device
// in a new slice is new, so naming them all would only repeat the
// count on the front of the line.
func TestSliceLogCreateListsTheTaintedDevices(t *testing.T) {
	capture := captureSliceLog(t)

	sliceLog.created(1, []SliceDevice{
		loggedDevice("device-a"),
		loggedDevice("device-b", disconnectedTaint, unusableTaint),
	})

	want := "slice: created generation 1, 2 devices, 1 tainted: device-b carries " +
		disconnectedTaint + ", " + unusableTaint
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

// The quiet line is what separates a live operator from a dead one, so
// it has to arrive, and it has to arrive rarely enough that a fleet of
// operators does not fill the log with it.
func TestSliceLogRateLimitsTheUnchangedLine(t *testing.T) {
	capture := captureSliceLog(t)
	devices := []SliceDevice{loggedDevice("device-a"), loggedDevice("device-b", disconnectedTaint)}

	// The first quiet pass prints at once, so a person who starts
	// reading a log does not wait for the interval to pass first.
	sliceLog.unchangedSlice(4, devices)
	want := "slice: unchanged at generation 4, 2 devices, 1 tainted (1 pass)"
	if got := capture.only(t); got != want {
		t.Fatalf("first line = %q, want %q", got, want)
	}

	// The backstop runs a pass every 60 seconds. Nine more of them
	// inside the interval add nothing.
	for range 9 {
		capture.advance(time.Minute)
		sliceLog.unchangedSlice(4, devices)
	}
	if lines := capture.lines(); len(lines) != 1 {
		t.Fatalf("the quiet interval printed %d lines: %q", len(lines), lines)
	}

	capture.advance(time.Minute)
	sliceLog.unchangedSlice(4, devices)
	lines := capture.lines()
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), lines)
	}
	// The pass count is what proves the loop ran the whole interval
	// instead of once.
	want = "slice: unchanged at generation 4, 2 devices, 1 tainted (10 passes)"
	if lines[1] != want {
		t.Errorf("second line = %q, want %q", lines[1], want)
	}
}

// A write already shows the operator is alive, so it starts the quiet
// interval again and no unchanged line follows straight after it.
func TestSliceLogWriteRestartsTheQuietInterval(t *testing.T) {
	capture := captureSliceLog(t)
	devices := []SliceDevice{loggedDevice("device-a")}

	sliceLog.unchangedSlice(4, devices)
	capture.advance(9 * time.Minute)
	sliceLog.wrote(5, nil, devices)
	capture.advance(2 * time.Minute)
	sliceLog.unchangedSlice(5, devices)

	lines := capture.lines()
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), lines)
	}
	want := "slice: wrote generation 5, 1 device, 0 tainted: device-a appeared"
	if lines[1] != want {
		t.Errorf("second line = %q, want %q", lines[1], want)
	}
}
