package main

// These tests run the kmsg relay against a scripted device: a read
// function that plays back records and errors the way /dev/kmsg
// would produce them. The device has one behavior that these tests
// cannot fake: blocking until the next record. This does not matter
// to the relay's logic, which only ever sees the results of read
// calls.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestParseKmsgRecord(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want kmsgRecord
	}{
		{
			name: "a kernel line",
			raw:  "6,339,5140900,-;NET: Registered PF_INET6 protocol family\n",
			want: kmsgRecord{Facility: 0, Severity: 6, Seq: 339, Stamp: 5140900 * time.Microsecond, Message: "NET: Registered PF_INET6 protocol family"},
		},
		{
			name: "a userspace line carries facility 1",
			raw:  "14,812,9000000,-;liken: hello from userspace\n",
			want: kmsgRecord{Facility: 1, Severity: 6, Seq: 812, Stamp: 9 * time.Second, Message: "liken: hello from userspace"},
		},
		{
			name: "continuation dictionary lines are dropped",
			raw:  "6,400,6000000,-;usb 1-1: new device\n SUBSYSTEM=usb\n DEVICE=+usb:1-1\n",
			want: kmsgRecord{Facility: 0, Severity: 6, Seq: 400, Stamp: 6 * time.Second, Message: "usb 1-1: new device"},
		},
		{
			name: "the fragment flag is not the parser's problem",
			raw:  "4,500,7000000,c;partial line the kernel chose to keep\n",
			want: kmsgRecord{Facility: 0, Severity: 4, Seq: 500, Stamp: 7 * time.Second, Message: "partial line the kernel chose to keep"},
		},
		{
			name: "a caller field after the flags",
			raw:  "6,600,8000000,-,caller=T1;modules loaded\n",
			want: kmsgRecord{Facility: 0, Severity: 6, Seq: 600, Stamp: 8 * time.Second, Message: "modules loaded"},
		},
		{
			name: "a higher facility (syslog daemons use these)",
			raw:  "30,700,9000000,-;something at facility 3\n",
			want: kmsgRecord{Facility: 3, Severity: 6, Seq: 700, Stamp: 9 * time.Second, Message: "something at facility 3"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseKmsgRecord([]byte(c.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got  %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestParseKmsgRecordRejectsMalformedRecords(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"no separator", "6,339,5140900,-"},
		{"too few header fields", "6,339;hello"},
		{"unparseable priority", "six,339,5140900,-;hello"},
		{"unparseable sequence", "6,later,5140900,-;hello"},
		{"unparseable timestamp", "6,339,soon,-;hello"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parseKmsgRecord([]byte(c.raw)); err == nil {
				t.Errorf("%q should not parse", c.raw)
			}
		})
	}
}

// scriptedDevice plays back a fixed sequence of reads. A string in
// the sequence is a record. An error in the sequence is returned
// as-is. When the script runs out, the device reports io.EOF. The
// relay treats this like any other unexpected error, and returns it.
// This is how the test ends a loop that never ends in production.
func scriptedDevice(events ...any) func([]byte) (int, error) {
	i := 0
	return func(buf []byte) (int, error) {
		if i >= len(events) {
			return 0, io.EOF
		}
		ev := events[i]
		i++
		switch ev := ev.(type) {
		case string:
			return copy(buf, ev), nil
		case error:
			return 0, ev
		}
		panic("scriptedDevice: events must be records or errors")
	}
}

// testKmsgRelay builds a relay over a scripted device with fixed
// clocks. The boot anchor sits at noon UTC, so a record stamped N
// seconds after boot converts to noon plus N seconds.
func testKmsgRelay(t *testing.T, facility int, read func([]byte) (int, error)) (*kmsgRelay, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	anchor := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	return &kmsgRelay{
		read:      read,
		facility:  facility,
		out:       newEnvelopeWriter(&out),
		cursorDir: t.TempDir(),
		anchor:    func() time.Time { return anchor },
		now:       func() time.Time { return anchor.Add(time.Hour) },
	}, &out
}

func TestKmsgRelayShipsItsFacilityOnly(t *testing.T) {
	relay, out := testKmsgRelay(t, 1, scriptedDevice(
		"6,100,1000000,-;kernel says hi\n",
		"14,101,2000000,-;liken: hello\n",
		"6,102,3000000,-;kernel again\n",
		"12,103,4000000,-;liken: a warning\n",
	))
	if err := relay.run(); err != io.EOF {
		t.Fatalf("run should end with the script's EOF, got %v", err)
	}
	es := parseEnvelopes(t, out.String())
	if len(es) != 2 {
		t.Fatalf("got %d envelopes, want 2 (facility 1 only): %+v", len(es), es)
	}
	if es[0].Message != "liken: hello" || es[0].Seq != 101 || es[0].Severity != "info" {
		t.Errorf("first: %+v", es[0])
	}
	if es[1].Message != "liken: a warning" || es[1].Severity != "warning" {
		t.Errorf("second: %+v", es[1])
	}
	if *es[0].Facility != 1 {
		t.Errorf("facility: %d", *es[0].Facility)
	}
}

func TestKmsgRelayConvertsStampsToWallTime(t *testing.T) {
	relay, out := testKmsgRelay(t, 0, scriptedDevice(
		"6,100,5000000,-;five seconds after boot\n",
	))
	if err := relay.run(); err != io.EOF {
		t.Fatal(err)
	}
	es := parseEnvelopes(t, out.String())
	if len(es) != 1 {
		t.Fatalf("got %d envelopes, want 1", len(es))
	}
	if es[0].Time != "2026-07-07T12:00:05Z" {
		t.Errorf("time: got %q, want the anchor plus five seconds", es[0].Time)
	}
}

func TestKmsgRelayReportsOverruns(t *testing.T) {
	relay, out := testKmsgRelay(t, 0, scriptedDevice(
		"6,100,1000000,-;before the overrun\n",
		syscall.EPIPE,
		"6,900,9000000,-;after the overrun\n",
	))
	if err := relay.run(); err != io.EOF {
		t.Fatal(err)
	}
	es := parseEnvelopes(t, out.String())
	if len(es) != 3 {
		t.Fatalf("got %d envelopes, want record, notice, record: %+v", len(es), es)
	}
	if es[1].Severity != "warning" || es[1].Message != "liken-logs: records were lost to a ring buffer overrun" {
		t.Errorf("notice: %+v", es[1])
	}
}

func TestKmsgRelayReportsUnparseableRecords(t *testing.T) {
	relay, out := testKmsgRelay(t, 0, scriptedDevice(
		"gibberish\n",
		"6,100,1000000,-;a good record\n",
	))
	if err := relay.run(); err != io.EOF {
		t.Fatal(err)
	}
	es := parseEnvelopes(t, out.String())
	if len(es) != 2 {
		t.Fatalf("got %d envelopes, want notice then record: %+v", len(es), es)
	}
	if es[0].Severity != "warning" {
		t.Errorf("notice: %+v", es[0])
	}
	if es[1].Message != "a good record" {
		t.Errorf("record: %+v", es[1])
	}
}

func TestKmsgRelayCheckpointsAndResumes(t *testing.T) {
	// Checkpoint on every record so the first run's position sticks.
	immediateCheckpoints(t)

	relay, _ := testKmsgRelay(t, 0, scriptedDevice(
		"6,100,1000000,-;first\n",
		"6,101,2000000,-;second\n",
	))
	if err := relay.run(); err != io.EOF {
		t.Fatal(err)
	}

	// The restarted relay reads the whole buffer again, because kmsg
	// cannot seek to a sequence number. It must skip every record at
	// or before the cursor.
	restarted, out := testKmsgRelay(t, 0, scriptedDevice(
		"6,100,1000000,-;first\n",
		"6,101,2000000,-;second\n",
		"6,102,3000000,-;third, the only new one\n",
	))
	restarted.cursorDir = relay.cursorDir
	if err := restarted.run(); err != io.EOF {
		t.Fatal(err)
	}
	es := parseEnvelopes(t, out.String())
	if len(es) != 2 {
		t.Fatalf("got %d envelopes, want the resume notice and one new record: %+v", len(es), es)
	}
	if es[0].Message != "liken-logs: resuming after sequence 101" {
		t.Errorf("notice: %+v", es[0])
	}
	if es[1].Seq != 102 || es[1].Message != "third, the only new one" {
		t.Errorf("record: %+v", es[1])
	}
}

// The cursor tracks the last sequence read, not the last sequence
// sent. Because of this, a liken relay that skips most kernel
// records still resumes past those records.
func TestKmsgRelayCursorAdvancesPastOtherFacilities(t *testing.T) {
	immediateCheckpoints(t)

	relay, _ := testKmsgRelay(t, 1, scriptedDevice(
		"14,100,1000000,-;liken: ours\n",
		"6,101,2000000,-;kernel: not ours\n",
	))
	if err := relay.run(); err != io.EOF {
		t.Fatal(err)
	}
	var cur kmsgCursor
	if !loadCursor(relay.cursorDir, &cur) {
		t.Fatal("no cursor saved")
	}
	if cur.Seq != 101 {
		t.Errorf("cursor should record the kernel record it read and skipped: got %d", cur.Seq)
	}
}

func TestKmsgRelayNoticesRecordsExpiredWhileDown(t *testing.T) {
	immediateCheckpoints(t)

	relay, _ := testKmsgRelay(t, 0, scriptedDevice("6,100,1000000,-;first\n"))
	if err := relay.run(); err != io.EOF {
		t.Fatal(err)
	}

	// While the relay was down, the buffer wrapped. The oldest
	// surviving record is far past the cursor.
	restarted, out := testKmsgRelay(t, 0, scriptedDevice("6,500,9000000,-;much later\n"))
	restarted.cursorDir = relay.cursorDir
	if err := restarted.run(); err != io.EOF {
		t.Fatal(err)
	}
	es := parseEnvelopes(t, out.String())
	if len(es) != 3 {
		t.Fatalf("got %d envelopes, want resume notice, expiry notice, record: %+v", len(es), es)
	}
	if es[1].Message != "liken-logs: records expired while the relay was down" {
		t.Errorf("expiry notice: %+v", es[1])
	}
	if es[2].Seq != 500 {
		t.Errorf("record: %+v", es[2])
	}
}

// A relay that cannot send records must exit with the write error,
// so the kubelet restarts it, instead of reading on and dropping
// records.
func TestKmsgRelayStopsWhenItsOutputFails(t *testing.T) {
	relay, _ := testKmsgRelay(t, 0, scriptedDevice("6,100,1000000,-;doomed\n"))
	relay.out = newEnvelopeWriter(brokenWriter{})
	if err := relay.run(); !errors.Is(err, errStdoutGone) {
		t.Errorf("run should surface the write error, got %v", err)
	}
}

// Checkpointing is what makes the relay durable. Because of this, a
// cursor directory that refuses writes must stop the relay, the same
// way a failed send does.
func TestKmsgRelayStopsWhenItCannotCheckpoint(t *testing.T) {
	relay, _ := testKmsgRelay(t, 0, scriptedDevice("6,100,1000000,-;x\n"))
	if err := os.Chmod(relay.cursorDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(relay.cursorDir, 0o700) })
	if err := relay.run(); !errors.Is(err, os.ErrPermission) {
		t.Errorf("run should surface the checkpoint error, got %v", err)
	}
}

// wallAnchor recovers the wall-clock moment of boot. Because of this,
// the result must land in the past. Adding the monotonic clock's
// elapsed time back to that result must land at the present moment.
func TestWallAnchorRecoversTheBootMoment(t *testing.T) {
	anchor := wallAnchor()
	if !anchor.Before(time.Now()) {
		t.Fatalf("the boot moment must be in the past: %v", anchor)
	}
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Duration(ts.Sec)*time.Second + time.Duration(ts.Nsec)
	if drift := time.Since(anchor.Add(elapsed)); drift < -time.Second || drift > time.Second {
		t.Errorf("anchor plus the monotonic elapsed should be now; off by %v", drift)
	}
}
