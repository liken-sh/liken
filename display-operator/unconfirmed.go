package main

// Writes that a device did not confirm.
//
// A panel can accept a write and then read back a different value, and
// a compositor can restart and serve a different mode than the one
// requested. Repeating such a write on every pass is visible on the
// screen as an input banner, a blank while the panel resyncs, or a
// restart of every screen on the card. So the operator makes each such
// write once per spec generation and records the result in the
// Display's status. A restarted operator reads that record from the
// API server and does not write again. An edit to spec raises the
// generation, which allows one more write.

import (
	"strconv"
	"strings"
	"time"
)

// DisplayUnconfirmed is one write that the device did not confirm.
// Control is the control's name as status.capabilities spells it, or
// mode for the screen's mode. Value is what the operator wrote, and
// Readback is what the device reported afterwards. Message is the
// error that explains why the operator stopped writing.
type DisplayUnconfirmed struct {
	Control    string `json:"control"`
	Value      string `json:"value"`
	Readback   string `json:"readback,omitempty"`
	Generation int64  `json:"generation"`
	Message    string `json:"message,omitempty"`
	Time       string `json:"time"`
}

// DisplayWritten counts the confirmed writes of one declared value to
// one control in one spec generation.
type DisplayWritten struct {
	Control    string `json:"control"`
	Value      string `json:"value"`
	Generation int64  `json:"generation"`
	Count      int    `json:"count"`
}

// writeBackLimit is how many times in one spec generation the operator
// writes a declared value back after the panel reports a different
// value. The first write does not count. Writing back restores a value
// that a person changed with the panel's buttons, and the limit stops
// the operator from fighting a panel that changes the value itself,
// such as one that switches to whichever input has a signal.
const writeBackLimit = 3

// modeControl is the control name that records a mode write. No DDC/CI
// control has this name, so mode records never collide with panel
// control records.
const modeControl = "mode"

// writeRecords holds the unconfirmed and confirmed writes of one Display
// during one pass. The pass starts from the records of the current
// generation in status, changes them as it writes, and publishes them to
// status.
type writeRecords struct {
	generation int64
	entries    []DisplayUnconfirmed
	written    []DisplayWritten
}

// writeRecordsOf reads the records in a Display's status that belong
// to its current generation, and drops the records of earlier
// generations.
func writeRecordsOf(display *Display) *writeRecords {
	records := &writeRecords{generation: display.Metadata.Generation}
	for _, entry := range display.Status.Unconfirmed {
		if entry.Generation == records.generation {
			records.entries = append(records.entries, entry)
		}
	}
	for _, entry := range display.Status.Written {
		if entry.Generation == records.generation {
			records.written = append(records.written, entry)
		}
	}
	return records
}

// sameValue compares a value recorded in status with a value computed
// in this pass, ignoring case. Status can hold a power name in
// lowercase that an earlier version of the operator wrote, and a
// mismatch would make the operator write to the panel again. No two
// values of one control differ only in case.
func sameValue(recorded, value string) bool {
	return strings.EqualFold(recorded, value)
}

// confirmedWrites returns how many confirmed writes of this value the
// control has had in this generation.
func (l *writeRecords) confirmedWrites(control, value string) int {
	for _, entry := range l.written {
		if entry.Control == control && sameValue(entry.Value, value) {
			return entry.Count
		}
	}
	return 0
}

// countWrite counts one confirmed write of this value to the control.
func (l *writeRecords) countWrite(control, value string) {
	for i, entry := range l.written {
		if entry.Control == control && sameValue(entry.Value, value) {
			l.written[i].Count++
			return
		}
	}
	l.written = append(l.written, DisplayWritten{
		Control: control, Value: value, Generation: l.generation, Count: 1,
	})
}

// publishedWrites returns the write counts for status, or nil when
// there are none.
func (l *writeRecords) publishedWrites() []DisplayWritten {
	if len(l.written) == 0 {
		return nil
	}
	return l.written
}

// isUnconfirmed reports whether the operator already wrote this value to the
// control in this generation and the device did not confirm it.
func (l *writeRecords) isUnconfirmed(control, value string) bool {
	for _, entry := range l.entries {
		if entry.Control == control && sameValue(entry.Value, value) {
			return true
		}
	}
	return false
}

// record stores one unconfirmed write. It replaces any earlier record
// of the same control, because a control has one declared value at a
// time.
func (l *writeRecords) record(control, value, readback string, failure error, now time.Time) {
	l.clear(control)
	entry := DisplayUnconfirmed{
		Control:    control,
		Value:      value,
		Readback:   readback,
		Generation: l.generation,
		Time:       now.UTC().Format(time.RFC3339),
	}
	if failure != nil {
		entry.Message = failure.Error()
	}
	l.entries = append(l.entries, entry)
}

// clear removes the record of one control. The operator calls it when
// the device already reports the declared value, whoever set it.
func (l *writeRecords) clear(control string) {
	kept := l.entries[:0:0]
	for _, entry := range l.entries {
		if entry.Control != control {
			kept = append(kept, entry)
		}
	}
	l.entries = kept
}

// publishedUnconfirmed returns the records for status, or nil when there
// are none.
func (l *writeRecords) publishedUnconfirmed() []DisplayUnconfirmed {
	if len(l.entries) == 0 {
		return nil
	}
	return l.entries
}

// statusValue formats a raw control value as status spells it: the
// published name for a control with named values, and the decimal
// number for any other control.
func statusValue(code byte, raw uint16) string {
	if _, named := coreValues[code]; named {
		return valueName(code, raw)
	}
	return strconv.Itoa(int(raw))
}
