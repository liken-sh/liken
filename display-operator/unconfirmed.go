package main

// Writes a device did not confirm.
//
// A panel can take a write and read back another value, and a
// compositor can start and serve another mode. A write repeated on
// every pass against such a device repeats what a person sees: an
// input banner, a resync blank, a restart of every screen on the
// card. So the operator makes such a write once for each spec
// generation and records the result in status. The record is in the
// API server and not in memory, so a restarted operator reads it and
// does not make the write again. An edit to spec raises the
// generation, and the new generation starts with no record.

import (
	"strconv"
	"time"
)

// One write the device did not confirm. Control names the control
// in status.capabilities' spelling, or mode for the screen's mode.
// Value is what the operator wrote, readback is what the device held
// after the write, and message is the failure in the words of the
// party that reported it.
type DisplayUnconfirmed struct {
	Control    string `json:"control"`
	Value      string `json:"value"`
	Readback   string `json:"readback,omitempty"`
	Generation int64  `json:"generation"`
	Message    string `json:"message,omitempty"`
	Time       string `json:"time"`
}

// One declared value the operator wrote in a spec generation, and how
// many writes the panel confirmed.
type DisplayWritten struct {
	Control    string `json:"control"`
	Value      string `json:"value"`
	Generation int64  `json:"generation"`
	Count      int    `json:"count"`
}

// How many times the operator writes a declared value back after the
// panel moved away from it, in one spec generation. The first write
// does not count. A person who changes a value at the panel's own
// buttons gets it written back, and a panel that switches the value by
// itself, such as a panel that moves to the input that carries a
// signal, stops getting writes after this many.
const writeBackLimit = 3

// The control name a mode write is recorded under. No DDC/CI control
// has this name, so the two kinds of write never share an entry.
const modeControl = "mode"

// The records of one Display for one pass. The pass starts from the
// records of the current generation, adds and removes records while
// it writes, and publishes the result in status.
type unconfirmedLedger struct {
	generation int64
	entries    []DisplayUnconfirmed
	written    []DisplayWritten
}

// The records that apply now. A record of an earlier generation was
// made against a spec that no longer stands, so the pass drops it.
func ledgerOf(display *Display) *unconfirmedLedger {
	ledger := &unconfirmedLedger{generation: display.Metadata.Generation}
	for _, entry := range display.Status.Unconfirmed {
		if entry.Generation == ledger.generation {
			ledger.entries = append(ledger.entries, entry)
		}
	}
	for _, entry := range display.Status.Written {
		if entry.Generation == ledger.generation {
			ledger.written = append(ledger.written, entry)
		}
	}
	return ledger
}

// How many confirmed writes of this value the control took in this
// generation.
func (l *unconfirmedLedger) writes(control, value string) int {
	for _, entry := range l.written {
		if entry.Control == control && entry.Value == value {
			return entry.Count
		}
	}
	return 0
}

// Count one confirmed write.
func (l *unconfirmedLedger) wrote(control, value string) {
	for i, entry := range l.written {
		if entry.Control == control && entry.Value == value {
			l.written[i].Count++
			return
		}
	}
	l.written = append(l.written, DisplayWritten{
		Control: control, Value: value, Generation: l.generation, Count: 1,
	})
}

// The counts as status publishes them, and nothing when there are
// none.
func (l *unconfirmedLedger) writtenPublished() []DisplayWritten {
	if len(l.written) == 0 {
		return nil
	}
	return l.written
}

// Whether the operator already wrote this value to this control in
// this generation, and the device did not confirm it.
func (l *unconfirmedLedger) declined(control, value string) bool {
	for _, entry := range l.entries {
		if entry.Control == control && entry.Value == value {
			return true
		}
	}
	return false
}

// Record one unconfirmed write. It replaces any record of the same
// control, because one control holds one pending value.
func (l *unconfirmedLedger) record(control, value, readback string, failure error, now time.Time) {
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

// Remove the record of one control. The operator does this when the
// device holds the declared value, whoever put it there.
func (l *unconfirmedLedger) clear(control string) {
	kept := l.entries[:0:0]
	for _, entry := range l.entries {
		if entry.Control != control {
			kept = append(kept, entry)
		}
	}
	l.entries = kept
}

// The records as status publishes them. No record publishes nothing,
// so a settled Display has no field and a steady pass writes nothing.
func (l *unconfirmedLedger) published() []DisplayUnconfirmed {
	if len(l.entries) == 0 {
		return nil
	}
	return l.entries
}

// One control's value in the spelling status uses: the published
// name for a control with named values, and the number for a
// continuous control.
func spokenValue(code byte, raw uint16) string {
	if _, named := coreValues[code]; named {
		return valueName(code, raw)
	}
	return strconv.Itoa(int(raw))
}
