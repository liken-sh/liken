package main

// Log lines for what each reconcile pass did to the ResourceSlice.
//
// A pass that finds the devices unchanged does not write the slice, so
// its resourceVersion and pool generation stay the same, as they do
// after the operator dies. The operator logs a line for those passes at
// least once per sliceLivenessInterval, so the log shows that it is
// still running.
//
// Only the reconcile loop's goroutine calls sliceReport, so it needs no
// lock.

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
// a slice line. The unchanged line prints on the first pass after the
// interval, so when the newest slice line is older than this plus one
// backstopInterval, the loop has stopped.
//
// The limit is a time and not a number of passes, because hardware
// events add passes to the backstop tick at no fixed rate.
const sliceLivenessInterval = 10 * time.Minute

var sliceLog = &sliceReport{out: os.Stdout, now: time.Now}

// unchanged counts the passes since lastLine that wrote nothing.
type sliceReport struct {
	out io.Writer
	now func() time.Time

	lastLine  time.Time
	unchanged int
}

func (r *sliceReport) created(generation int64, devices []SliceDevice) {
	r.line("slice: created generation %d, %s%s",
		generation, sliceSummary(devices), tail(taintedPhrases(devices)))
}

// wrote names each device that changed, because a device that gains or
// loses a taint leaves the device count the same, and a new taint keeps
// a claim Pending or evicts its pod.
func (r *sliceReport) wrote(generation int64, published, current []SliceDevice) {
	r.line("slice: wrote generation %d, %s%s",
		generation, sliceSummary(current), tail(sliceChanges(published, current)))
}

// unchangedSlice logs at most once per sliceLivenessInterval. The pass
// count in the line shows that the loop ran throughout the interval.
func (r *sliceReport) unchangedSlice(generation int64, devices []SliceDevice) {
	r.unchanged++
	if r.now().Sub(r.lastLine) < sliceLivenessInterval {
		return
	}
	r.line("slice: unchanged at generation %d, %s (%d %s)",
		generation, sliceSummary(devices), r.unchanged, plural(r.unchanged, "pass", "passes"))
}

// line restarts the interval for every kind of line, because a created
// or wrote line also shows that the operator is running.
func (r *sliceReport) line(format string, args ...any) {
	fmt.Fprintf(r.out, format+"\n", args...)
	r.lastLine = r.now()
	r.unchanged = 0
}

// sliceSummary includes the tainted count, because a slice whose
// devices are all tainted offers nothing.
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

// sliceChanges matches devices by name and does not walk the two lists
// in step, because the published list comes from the API server, where
// another writer can have changed its order or contents.
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
			phrases = append(phrases, deviceChanges(was, is)...)
		}
	}
	return phrases
}

// appeared names the taints of a new device, because a device that
// arrives tainted cannot serve claims yet.
func appeared(device SliceDevice) string {
	keys := taintKeys(device.Taints)
	if len(keys) == 0 {
		return device.Name + " appeared"
	}
	return device.Name + " appeared with " + strings.Join(keys, ", ")
}

// deviceChanges describes a device that was already in the slice. A
// device can gain one taint and lose another in the same pass, so it
// returns a phrase for each.
//
// An attribute change gets a phrase only when no taint changed, and it
// does not name the attributes. The taint changes matter more, and the
// slice has the attribute values.
func deviceChanges(was, is SliceDevice) []string {
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

// taintedPhrases lists only the tainted devices of a new slice, because
// every device in it is new.
func taintedPhrases(devices []SliceDevice) []string {
	var phrases []string
	for _, device := range devices {
		if keys := taintKeys(device.Taints); len(keys) > 0 {
			phrases = append(phrases, device.Name+" has "+strings.Join(keys, ", "))
		}
	}
	return phrases
}

// taintKeys compares keys only, because this operator publishes one
// effect for each key. The sort keeps the line the same in whatever
// order the API server returns the taints.
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

// tail separates the phrases with semicolons, because commas already
// separate the taint keys inside a phrase.
func tail(phrases []string) string {
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
