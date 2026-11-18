package main

// The operator logs what each pass did to the ResourceSlice.
//
// A pass that finds no change writes nothing to the API server, so a
// slice left behind by a dead operator looks the same in the API as
// the slice of a running one. The operator prints a line for each
// write, and a line at a fixed interval while nothing changes, so the
// log shows whether it is running.
//
// Only the reconcile loop calls the reporter, from one goroutine, so
// its fields need no lock.

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"
)

// sliceLivenessInterval is the longest a running operator goes without
// printing a slice line. When the newest slice line in the log is
// older than this, the reconcile loop is not running.
//
// The limit is a time and not a count of passes, because the pass rate
// varies from one pass each backstopInterval to many a second while a
// cable flaps.
const sliceLivenessInterval = 10 * time.Minute

// sliceLog prints to stdout, like the startup lines and the hardware
// events.
var sliceLog = &sliceReport{out: os.Stdout, now: time.Now}

type sliceReport struct {
	out io.Writer
	now func() time.Time

	// unchanged counts the passes since lastLine that wrote nothing.
	lastLine  time.Time
	unchanged int
}

// created reports the create of the slice, at the operator's first
// pass on a node or after the slice was deleted with its Node.
func (r *sliceReport) created(generation int64, devices []SliceDevice) {
	r.line("slice: created generation %d, %s%s",
		generation, sliceSummary(devices), phraseList(taintedPhrases(devices)))
}

// wrote reports an update of the slice and lists what changed. A
// device that gains or loses a taint leaves the device count the same,
// and that change can keep a pod from being scheduled or evict it, so
// the line names it.
func (r *sliceReport) wrote(generation int64, published, current []SliceDevice) {
	r.line("slice: wrote generation %d, %s%s",
		generation, sliceSummary(current), phraseList(sliceChanges(published, current)))
}

// unchangedSlice reports a pass that wrote nothing, at most once per
// sliceLivenessInterval, with the count of passes since the last line.
func (r *sliceReport) unchangedSlice(generation int64, devices []SliceDevice) {
	r.unchanged++
	if r.now().Sub(r.lastLine) < sliceLivenessInterval {
		return
	}
	r.line("slice: unchanged at generation %d, %s (%d %s)",
		generation, sliceSummary(devices), r.unchanged, plural(r.unchanged, "pass", "passes"))
}

// line prints one line and restarts the interval. A write line also
// shows that the operator is running, so no unchanged line follows
// right after it.
func (r *sliceReport) line(format string, args ...any) {
	fmt.Fprintf(r.out, format+"\n", args...)
	r.lastLine = r.now()
	r.unchanged = 0
}

// sliceSummary returns the device count and the count of tainted
// devices, because a slice whose devices are all tainted offers
// nothing to allocate.
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

// sliceChanges returns phrases for the devices that were added,
// removed, or changed between the published slice and the one this
// pass writes. It matches devices by name and not by position, because
// another client can have changed the order of the published list.
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
			phrases = append(phrases, addedPhrase(is))
		case !has:
			phrases = append(phrases, name+" left")
		default:
			phrases = append(phrases, changedPhrases(was, is)...)
		}
	}
	return phrases
}

// addedPhrase lists the taints of a new device, because a tainted
// device cannot be allocated yet.
func addedPhrase(device SliceDevice) string {
	keys := taintKeys(device.Taints)
	if len(keys) == 0 {
		return device.Name + " appeared"
	}
	return device.Name + " appeared with " + strings.Join(keys, ", ")
}

// changedPhrases describes a device that was already in the slice. A
// change to attributes alone gets one phrase that does not list them,
// because a list of attributes would hide the taint changes, which
// affect scheduling.
func changedPhrases(was, is SliceDevice) []string {
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

// taintedPhrases returns a phrase for each device that has taints. The
// line of a created slice lists only these, because every device in
// it is new and the count at the start of the line already covers
// them.
func taintedPhrases(devices []SliceDevice) []string {
	var phrases []string
	for _, device := range devices {
		if keys := taintKeys(device.Taints); len(keys) > 0 {
			phrases = append(phrases, device.Name+" has "+strings.Join(keys, ", "))
		}
	}
	return phrases
}

// taintKeys returns the sorted keys of a device's taints. Keys alone
// are enough because this operator writes one effect for each key.
func taintKeys(taints []DeviceTaint) []string {
	keys := make([]string, 0, len(taints))
	for _, taint := range taints {
		keys = append(keys, taint.Key)
	}
	slices.Sort(keys)
	return keys
}

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

// phraseList formats phrases for the end of a line, or returns an
// empty string when there are none.
func phraseList(phrases []string) string {
	if len(phrases) == 0 {
		return ""
	}
	return ": " + strings.Join(phrases, "; ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
