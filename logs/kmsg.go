package main

// The kernel's log buffer has a readable device, /dev/kmsg, and its
// format is the one piece of parsing the kernel relays own. Each
// read(2) returns exactly one record:
//
//	priority,sequence,timestamp,flags[,caller];message
//	 SUBSYSTEM=...
//	 DEVICE=...
//
// The priority byte packs two syslog facts: facility<<3 | severity.
// Facility is what separates the machine's two streams sharing this
// buffer: records the kernel printed carry facility 0, and records
// userspace wrote through /dev/kmsg (init's own lines) carry
// facility 1. One relay ships one facility, so "which program said
// this" is answered by which pod the line came from, not by string
// matching.
//
// The sequence number is the buffer's own ordering, and it counts
// every record regardless of facility. Gaps within one relay's
// output are therefore normal (the other facility's records consumed
// those numbers); actual loss is detectable, because a reader
// that has fallen behind a wrapping buffer gets EPIPE from read(2)
// and a notice goes into the stream. The timestamp is microseconds
// since boot on the kernel's monotonic-ish printk clock, converted
// to wall time here by anchoring against the current clocks.
//
// The device blocks a caught-up reader until the next record, so the
// read loop is also the follow mechanism; there is no polling. A new
// reader starts at the oldest record the buffer still holds, which
// is what makes a fresh pod replay the boot from its head. The
// device cannot seek to a sequence number, so resuming from a cursor
// means reading from the oldest record and skipping until the cursor
// passes.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/chrisguidry/liken/machine"
)

const kmsgPath = "/dev/kmsg"

// kmsgRecord is one parsed record: the header's facts plus the
// message line, verbatim. Continuation lines (the SUBSYSTEM=/DEVICE=
// dictionary some records append) are device metadata, not part of
// the message, and are dropped.
type kmsgRecord struct {
	Facility int
	Severity int
	Seq      uint64
	Stamp    time.Duration // since boot
	Message  string
}

func parseKmsgRecord(raw []byte) (kmsgRecord, error) {
	semi := bytes.IndexByte(raw, ';')
	if semi < 0 {
		return kmsgRecord{}, fmt.Errorf("no header separator in %q", raw)
	}
	fields := strings.Split(string(raw[:semi]), ",")
	if len(fields) < 4 {
		return kmsgRecord{}, fmt.Errorf("header %q has %d fields, want at least 4", raw[:semi], len(fields))
	}
	prio, err := strconv.Atoi(fields[0])
	if err != nil || prio < 0 {
		return kmsgRecord{}, fmt.Errorf("bad priority in header %q", raw[:semi])
	}
	seq, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return kmsgRecord{}, fmt.Errorf("bad sequence in header %q", raw[:semi])
	}
	us, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || us < 0 {
		return kmsgRecord{}, fmt.Errorf("bad timestamp in header %q", raw[:semi])
	}
	message := raw[semi+1:]
	if nl := bytes.IndexByte(message, '\n'); nl >= 0 {
		message = message[:nl]
	}
	return kmsgRecord{
		Facility: prio >> 3,
		Severity: prio & 7,
		Seq:      seq,
		Stamp:    time.Duration(us) * time.Microsecond,
		Message:  string(message),
	}, nil
}

// kmsgCursor is the resume point: the last sequence number this
// relay has read (read, not shipped, so a restart doesn't re-scan
// the other facility's records either).
type kmsgCursor struct {
	Seq uint64 `json:"seq"`
}

// kmsgRelay follows one facility of the kernel buffer. The read
// function is /dev/kmsg's in production and a scripted fixture in
// tests; anchor reports the wall-clock moment of boot as the clocks
// currently stand.
type kmsgRelay struct {
	read      func([]byte) (int, error)
	facility  int
	out       *envelopeWriter
	cursorDir string
	anchor    func() time.Time
	now       func() time.Time
}

func (r *kmsgRelay) run() error {
	var cur kmsgCursor
	resuming := loadCursor(r.cursorDir, &cur)
	if resuming {
		_ = r.out.notice(r.now(), "info", cur.Seq, &r.facility,
			fmt.Sprintf("resuming after sequence %d", cur.Seq))
	}

	var lastCheckpoint time.Time
	// first marks the first record parsed after a resume, the only
	// moment when a jump past the cursor reveals expired records.
	first := true
	buf := make([]byte, 8192)
	for {
		n, err := r.read(buf)
		if err != nil {
			// EPIPE is the kernel's overrun signal: the buffer
			// wrapped past our position. The read position has
			// already been moved to the oldest surviving record, so
			// the only job here is to record that a gap happened.
			if errors.Is(err, syscall.EPIPE) {
				_ = r.out.notice(r.now(), "warning", cur.Seq, &r.facility,
					"records were lost to a ring buffer overrun")
				continue
			}
			return err
		}
		rec, err := parseKmsgRecord(buf[:n])
		if err != nil {
			_ = r.out.notice(r.now(), "warning", cur.Seq, &r.facility,
				"unparseable record: "+err.Error())
			continue
		}

		if resuming {
			// The buffer may have discarded records past the cursor
			// while the relay was down; that is the same loss as an
			// overrun and gets the same notice.
			if first && rec.Seq > cur.Seq+1 {
				_ = r.out.notice(r.now(), "warning", cur.Seq, &r.facility,
					"records expired while the relay was down")
			}
			first = false
			if rec.Seq <= cur.Seq {
				continue
			}
			resuming = false
		}

		cur.Seq = rec.Seq
		if rec.Facility == r.facility {
			if err := r.out.emit(envelope{
				Time:     r.anchor().Add(rec.Stamp).UTC().Format(time.RFC3339Nano),
				Severity: machine.SeverityNames[rec.Severity],
				Facility: &r.facility,
				Seq:      rec.Seq,
				Message:  rec.Message,
			}); err != nil {
				return err
			}
		}

		if now := r.now(); now.Sub(lastCheckpoint) >= checkpointInterval {
			lastCheckpoint = now
			if err := saveCursor(r.cursorDir, cur); err != nil {
				return err
			}
		}
	}
}

// wallAnchor computes the wall-clock time of boot from the current
// clocks: CLOCK_REALTIME minus CLOCK_MONOTONIC. A record's wall time
// is then anchor + its since-boot stamp. Sampling per record (the
// two reads are vDSO calls, effectively free) means the conversion
// always uses the clock as it stands now, so even records from
// before init's one boot-time clock step land on the corrected
// timeline: the step moved CLOCK_REALTIME, and the stamps are
// monotonic.
func wallAnchor() time.Time {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		// Without the monotonic clock (which cannot really happen on
		// Linux), record times degrade to relay time.
		return time.Now()
	}
	return time.Now().Add(-(time.Duration(ts.Sec)*time.Second + time.Duration(ts.Nsec)))
}

// relayKmsg is the kernel and liken verbs: follow /dev/kmsg,
// shipping one facility. Opening the device takes real privilege:
// the kernel demands CAP_SYSLOG (CONFIG_SECURITY_DMESG_RESTRICT is
// set), and the container runtime's devices cgroup separately gates
// the open, which is why the two kmsg containers run privileged
// (logs/manifests/logs.yaml tells that story).
func relayKmsg(facility int) error {
	f, err := os.Open(kmsgPath)
	if err != nil {
		return err
	}
	defer f.Close()
	relay := &kmsgRelay{
		read:      f.Read,
		facility:  facility,
		out:       newEnvelopeWriter(os.Stdout),
		cursorDir: cursorDir,
		anchor:    wallAnchor,
		now:       time.Now,
	}
	return relay.run()
}
