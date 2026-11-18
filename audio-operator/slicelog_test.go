package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// secondTaint gives the tests a second taint key. Any key would do.
const secondTaint = noMonitorTaint

// sliceLogCapture replaces sliceLog with a reporter that writes to a
// buffer and reads a clock that the test advances.
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

// only returns the reporter's line and fails the test unless it
// printed exactly one.
func (c *sliceLogCapture) only(t *testing.T) string {
	t.Helper()
	lines := c.lines()
	if len(lines) != 1 {
		t.Fatalf("the reporter printed %d lines, want 1: %q", len(lines), lines)
	}
	return lines[0]
}

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
			published: []SliceDevice{loggedDevice("device-a", disconnectedTaint, secondTaint), loggedDevice("device-b")},
			current:   []SliceDevice{loggedDevice("device-a"), loggedDevice("device-b")},
			want: "slice: wrote generation 5, 2 devices, 0 tainted: device-a lost " +
				disconnectedTaint + ", " + secondTaint,
		},
		{
			name:      "a device gained one taint and lost another",
			published: []SliceDevice{loggedDevice("device-a", disconnectedTaint)},
			current:   []SliceDevice{loggedDevice("device-a", secondTaint)},
			want: "slice: wrote generation 5, 1 device, 1 tainted: device-a gained " +
				secondTaint + "; device-a lost " + disconnectedTaint,
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
			current:   []SliceDevice{loggedDevice("device-a"), loggedDevice("device-b", secondTaint)},
			want:      "slice: wrote generation 5, 2 devices, 1 tainted: device-b appeared with " + secondTaint,
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

func TestSliceLogCreateListsTheTaintedDevices(t *testing.T) {
	capture := captureSliceLog(t)

	sliceLog.created(1, []SliceDevice{
		loggedDevice("device-a"),
		loggedDevice("device-b", disconnectedTaint, secondTaint),
	})

	want := "slice: created generation 1, 2 devices, 1 tainted: device-b has " +
		disconnectedTaint + ", " + secondTaint
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestSliceLogRateLimitsTheUnchangedLine(t *testing.T) {
	capture := captureSliceLog(t)
	devices := []SliceDevice{loggedDevice("device-a"), loggedDevice("device-b", disconnectedTaint)}

	// The first unchanged pass prints at once, so a reader does not wait
	// a whole interval to see the operator's state.
	sliceLog.unchangedSlice(4, devices)
	want := "slice: unchanged at generation 4, 2 devices, 1 tainted (1 pass)"
	if got := capture.only(t); got != want {
		t.Fatalf("first line = %q, want %q", got, want)
	}

	// Nine passes a minute apart stay inside sliceLivenessInterval and
	// print nothing.
	for range 9 {
		capture.advance(time.Minute)
		sliceLog.unchangedSlice(4, devices)
	}
	if lines := capture.lines(); len(lines) != 1 {
		t.Fatalf("the quiet interval printed %d lines, want 1: %q", len(lines), lines)
	}

	capture.advance(time.Minute)
	sliceLog.unchangedSlice(4, devices)
	lines := capture.lines()
	if len(lines) != 2 {
		t.Fatalf("the reporter printed %d lines, want 2: %q", len(lines), lines)
	}
	want = "slice: unchanged at generation 4, 2 devices, 1 tainted (10 passes)"
	if lines[1] != want {
		t.Errorf("second line = %q, want %q", lines[1], want)
	}
}

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
		t.Fatalf("the reporter printed %d lines, want 2: %q", len(lines), lines)
	}
	want := "slice: wrote generation 5, 1 device, 0 tainted: device-a appeared"
	if lines[1] != want {
		t.Errorf("second line = %q, want %q", lines[1], want)
	}
}
