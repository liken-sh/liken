package main

// A pass that finds the slice unchanged does not write it, so the
// slice's resourceVersion and generation look the same whether the
// operator is running or has stopped. The operator therefore also logs
// an unchanged pass, at most once per sliceLivenessInterval.
//
// Only the reconcile loop's goroutine calls sliceReport, so its fields
// need no lock.

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
// a slice line. When the newest slice line is older than this, the
// reconcile loop has stopped.
//
// The limit is a time and not a number of passes, because the pass rate
// varies from one a minute (backstopInterval) to many a second while a
// controller reconnects in a loop.
const sliceLivenessInterval = 10 * time.Minute

var sliceLog = &sliceReport{out: os.Stdout, now: time.Now}

type sliceReport struct {
	out io.Writer
	now func() time.Time

	lastLine time.Time
	// unchanged counts the passes since lastLine that changed nothing.
	unchanged int
}

// created logs the creation of the slice, when the API server had none
// for this node.
func (r *sliceReport) created(generation int64, devices []SliceDevice) {
	r.line("slice: created generation %d, %s%s",
		generation, sliceSummary(devices), tail(taintedPhrases(devices)))
}

// wrote logs an update of the slice and lists each device that
// changed. The counts at the start of the line do not show which device
// gained or lost a taint, and a taint keeps a pod from being scheduled
// or evicts it.
func (r *sliceReport) wrote(generation int64, published, current []SliceDevice) {
	r.line("slice: wrote generation %d, %s%s",
		generation, sliceSummary(current), tail(sliceChanges(published, current)))
}

// deletedSlice logs the deletion of the slice, when the node has no
// devices to publish.
func (r *sliceReport) deletedSlice() {
	r.line("slice: deleted the ResourceSlice, because the node has no device to publish")
}

// unchangedSlice logs a pass that wrote nothing, at most once per
// sliceLivenessInterval. The first call logs at once. The line gives the
// number of passes since the last line, which shows that the loop kept
// running through the interval.
func (r *sliceReport) unchangedSlice(generation int64, devices []SliceDevice) {
	r.unchanged++
	if r.now().Sub(r.lastLine) < sliceLivenessInterval {
		return
	}
	r.line("slice: unchanged at generation %d, %s (%d %s)",
		generation, sliceSummary(devices), r.unchanged, plural(r.unchanged, "pass", "passes"))
}

// line prints a line and restarts the interval of unchangedSlice,
// because any line shows that the operator is running.
func (r *sliceReport) line(format string, args ...any) {
	fmt.Fprintf(r.out, format+"\n", args...)
	r.lastLine = r.now()
	r.unchanged = 0
}

// sliceSummary gives the number of devices and the number with a taint,
// because a slice whose devices are all tainted offers nothing usable.
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

// sliceChanges returns a phrase for each change between the published
// devices and the devices this pass writes. It matches devices by name,
// because another client can have written the published slice in a
// different order.
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

// appeared describes a device that was not in the published slice,
// with its taints, so a new device that is already unusable shows as
// such.
func appeared(device SliceDevice) string {
	keys := taintKeys(device.Taints)
	if len(keys) == 0 {
		return device.Name + " appeared"
	}
	return device.Name + " appeared with " + strings.Join(keys, ", ")
}

// deviceChanges describes how a device that was already in the
// published slice changed: the taints it gained, the taints it lost, or,
// when its taints are the same, "changed attributes". The attributes
// are not listed, so they do not hide the taint changes in the line.
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

// taintedPhrases returns a phrase for each device that has taints. A
// created slice lists only these, because every device in it is new.
func taintedPhrases(devices []SliceDevice) []string {
	var phrases []string
	for _, device := range devices {
		if keys := taintKeys(device.Taints); len(keys) > 0 {
			phrases = append(phrases, device.Name+" has "+strings.Join(keys, ", "))
		}
	}
	return phrases
}

// taintKeys returns the sorted keys of a device's taints. Comparing keys
// is enough, because on one device this operator always gives a key the
// same effect. Sorting keeps the line the same whatever order the taints
// are stored in.
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

// tail formats phrases for the end of a log line, or returns "" when
// there are none.
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
