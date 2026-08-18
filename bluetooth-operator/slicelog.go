package main

// Reporting what each pass did to the slice.
//
// A pass that finds nothing changed writes nothing. That is correct
// for the API server, but a silent log is not enough on its own.
// The slice's resourceVersion and its pool generation do not change
// while an operator republishes the same content, and they do not
// change after the operator dies and leaves its last slice behind. A
// person who reads the API sees the same two numbers in both cases.
// Only the operator can tell the two apart, so it prints which one it
// is.
//
// The reporter holds what that needs: the time of the last line it
// printed, and how many passes have changed nothing since then. One
// goroutine runs the reconcile loop in this operator, and it is the
// only caller, so the counters need no lock.

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"
)

// sliceLivenessInterval is the longest a running operator stays quiet
// about its slice. A person who reads the log and finds the newest
// slice line older than this knows the loop is not running, whatever
// the API server still shows.
//
// The bound is time, not a count of passes. The loop's rate sets how
// fast passes accumulate: the backstop tick alone gives one pass a
// minute, and a controller that reconnects in a loop gives many passes
// a second. So a line on every tenth pass says nothing fixed about how
// old the newest line may be. Ten minutes against a 60 second backstop is one
// line for every ten quiet passes, and it keeps that limit whatever
// the hardware does.
const sliceLivenessInterval = 10 * time.Minute

// sliceLog is where this operator reports its slice writes. Routine
// facts go to stdout, the same as the startup lines and the hardware
// events, and the failures around them still go to stderr.
var sliceLog = &sliceReport{out: os.Stdout, now: time.Now}

type sliceReport struct {
	out io.Writer
	now func() time.Time

	lastLine  time.Time
	unchanged int
}

// created reports the first write, which puts the slice in the API for
// the first time or replaces one that a node deletion took away.
func (r *sliceReport) created(generation int64, devices []SliceDevice) {
	r.line("slice: created generation %d, %s%s",
		generation, sliceSummary(devices), tail(taintedPhrases(devices)))
}

// wrote reports a replacement and names what moved. The device count
// alone does not name it: a device that gains or loses a taint keeps
// the count where it was. That taint change is the event that parks a
// consumer or evicts one, so the line names it.
func (r *sliceReport) wrote(generation int64, published, current []SliceDevice) {
	r.line("slice: wrote generation %d, %s%s",
		generation, sliceSummary(current), tail(sliceChanges(published, current)))
}

// deletedSlice reports the one removal this operator makes, which is
// the last controller being unpaired. Nothing is left to count, so the
// line has no summary.
func (r *sliceReport) deletedSlice() {
	r.line("slice: deleted, the last paired controller is gone")
}

// unchangedSlice reports a pass that wrote nothing, at most once per
// sliceLivenessInterval. The pass count on the line proves the loop
// ran the whole time instead of once.
func (r *sliceReport) unchangedSlice(generation int64, devices []SliceDevice) {
	r.unchanged++
	if r.now().Sub(r.lastLine) < sliceLivenessInterval {
		return
	}
	r.line("slice: unchanged at generation %d, %s (%d %s)",
		generation, sliceSummary(devices), r.unchanged, plural(r.unchanged, "pass", "passes"))
}

// line prints one line and starts the quiet interval again. A write
// already shows the operator is alive, so it resets the interval the
// same way a liveness line does, and no unchanged line follows
// straight after a write.
func (r *sliceReport) line(format string, args ...any) {
	fmt.Fprintf(r.out, format+"\n", args...)
	r.lastLine = r.now()
	r.unchanged = 0
}

// sliceSummary counts what the slice now offers. The tainted count is
// the second half because a slice of three devices with all three
// tainted offers nothing, and the count is the shortest way to say so.
func sliceSummary(devices []SliceDevice) string {
	tainted := 0
	for _, device := range devices {
		if len(device.Taints) > 0 {
			tainted++
		}
	}
	return fmt.Sprintf("%d %s, %d tainted",
		len(devices), plural(len(devices), "device", "devices"), tainted)
}

// sliceChanges names every device that moved between the published
// slice and the one this pass writes, one phrase for each.
//
// It reads both lists into maps rather than walking them in step,
// because the published list comes back from the API server and this
// operator is not the only thing that can write it.
func sliceChanges(published, current []SliceDevice) []string {
	before := devicesByName(published)
	after := devicesByName(current)

	names := make([]string, 0, len(before)+len(after))
	for name := range before {
		names = append(names, name)
	}
	for name := range after {
		if _, seen := before[name]; !seen {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	phrases := make([]string, 0, len(names))
	for _, name := range names {
		was, had := before[name]
		is, has := after[name]
		switch {
		case !had:
			phrases = append(phrases, appeared(is))
		case !has:
			phrases = append(phrases, name+" left")
		default:
			phrases = append(phrases, moved(was, is)...)
		}
	}
	return phrases
}

// appeared describes a device the slice did not hold before, with the
// taints it arrives with, because a device that appears already unable
// to serve is a different fact from one that appears ready.
func appeared(device SliceDevice) string {
	keys := taintKeys(device.Taints)
	if len(keys) == 0 {
		return device.Name + " appeared"
	}
	return device.Name + " appeared with " + strings.Join(keys, ", ")
}

// moved describes what changed about a device the slice held
// before. A device can gain one taint and lose another in one pass, so
// this gives back up to two phrases.
//
// An attribute change with no taint change gets one flat phrase and no
// detail. The attributes say what the hardware is, so a reader who
// needs them reads the slice. Naming every one of them here would
// hide the taint changes, which are what park a consumer or evict one.
func moved(was, is SliceDevice) []string {
	gained := keysNotIn(taintKeys(is.Taints), taintKeys(was.Taints))
	lost := keysNotIn(taintKeys(was.Taints), taintKeys(is.Taints))

	var phrases []string
	if len(gained) > 0 {
		phrases = append(phrases, is.Name+" gained "+strings.Join(gained, ", "))
	}
	if len(lost) > 0 {
		phrases = append(phrases, is.Name+" lost "+strings.Join(lost, ", "))
	}
	if len(phrases) == 0 && !reflect.DeepEqual(was.Attributes, is.Attributes) {
		phrases = append(phrases, is.Name+" changed attributes")
	}
	return phrases
}

// taintedPhrases lists the devices that have taints, which is what a
// newly created slice has to say. Every device in it is new, so naming
// them all would only repeat the count on the front of the line.
func taintedPhrases(devices []SliceDevice) []string {
	var phrases []string
	for _, device := range devices {
		if keys := taintKeys(device.Taints); len(keys) > 0 {
			phrases = append(phrases, device.Name+" has "+strings.Join(keys, ", "))
		}
	}
	return phrases
}

// taintKeys gives the sorted keys of one device's taints. The key is
// the whole comparison: this operator publishes one effect per key, so
// a key that arrives or leaves is the whole change. Sorting keeps the
// line the same whatever order the API server stored them in.
func taintKeys(taints []DeviceTaint) []string {
	keys := make([]string, 0, len(taints))
	for _, taint := range taints {
		keys = append(keys, taint.Key)
	}
	slices.Sort(keys)
	return keys
}

// keysNotIn gives the keys of the first list that the second does not
// hold.
func keysNotIn(keys, other []string) []string {
	var out []string
	for _, key := range keys {
		if !slices.Contains(other, key) {
			out = append(out, key)
		}
	}
	return out
}

func devicesByName(devices []SliceDevice) map[string]SliceDevice {
	out := make(map[string]SliceDevice, len(devices))
	for _, device := range devices {
		out[device.Name] = device
	}
	return out
}

// tail joins the phrases onto the end of a line, and gives an empty
// string when there are none. The separators nest: a colon opens the
// list, a semicolon divides the devices, and a comma divides one
// device's taint keys.
func tail(phrases []string) string {
	if len(phrases) == 0 {
		return ""
	}
	return ": " + strings.Join(phrases, "; ")
}

// plural picks the word that goes with a count, so a line reads
// "1 device" and never "1 devices".
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
