package main

// The lines a person reads to follow one receiver: each command the
// operator sends it, what asked for the command, and what the receiver
// reported back. A command's line waits for the receiver's report, so
// one line holds the whole operation. Nothing here logs a poll, a
// status write, or a line the receiver sends by itself.

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

// How long a command's line waits for the receiver to report the
// result. A Denon echoes a command within a fraction of a second, and a
// WiiM reports a setting at its next poll, which is ten seconds away at
// most.
var receiverConfirmWait = 15 * time.Second

// receiverLog writes one receiver's lines, each with the Receiver's
// name first, as the node workload writes each CECBus line.
type receiverLog struct {
	to   io.Writer
	name string

	mutex sync.Mutex
	// pending holds the lines that wait for the receiver's report.
	pending []*pendingLine
	// said holds the last value each repeated command logged, so a
	// command that a pass sends again with the same value logs nothing.
	said map[string]any
}

func newReceiverLog(to io.Writer, name string) *receiverLog {
	return &receiverLog{to: to, name: name, said: map[string]any{}}
}

// pendingLine is one command's line while it waits. check answers what
// the receiver reports now and whether that is the result the command
// asked for.
type pendingLine struct {
	line  string
	began time.Time
	check func() (string, bool)
	timer *time.Timer
}

func (l *receiverLog) printf(format string, args ...any) {
	fmt.Fprintf(l.to, "Receiver %s: %s\n", l.name, fmt.Sprintf(format, args...))
}

// refused writes the line of a command the driver or the receiver
// refused, with the driver's own error text.
func (l *receiverLog) refused(line string, err error) {
	l.printf("%s; the command failed: %v", line, err)
}

// confirm writes line once check reports the result, or once
// receiverConfirmWait ends without it. began is when the command went
// out, so the line states how long the receiver took. The line joins
// the pending ones before the first check, so a report that arrives
// between the command and this call still ends it.
func (l *receiverLog) confirm(line string, began time.Time, check func() (string, bool)) {
	waiting := &pendingLine{line: line, began: began, check: check}
	l.mutex.Lock()
	waiting.timer = time.AfterFunc(receiverConfirmWait, func() { l.expire(waiting) })
	l.pending = append(l.pending, waiting)
	l.mutex.Unlock()
	l.observe()
}

// observe checks every pending line against what the receiver reports
// now. The unit calls it for each line the receiver sends. The checks
// run outside the mutex, because each one reads the driver's state.
func (l *receiverLog) observe() {
	l.mutex.Lock()
	waiting := append([]*pendingLine(nil), l.pending...)
	l.mutex.Unlock()
	for _, line := range waiting {
		report, confirmed := line.check()
		if confirmed && l.take(line) {
			line.timer.Stop()
			l.printf("%s; the receiver reported %s after %s", line.line, report, elapsed(time.Since(line.began)))
		}
	}
}

// expire writes a line whose report did not arrive in time.
func (l *receiverLog) expire(line *pendingLine) {
	if !l.take(line) {
		return
	}
	report, _ := line.check()
	l.printf("%s; the receiver did not report it in %s, and it reports %s", line.line, elapsed(receiverConfirmWait), report)
}

// take removes a pending line and answers whether this call removed
// it, so a line prints once when a report and the timer arrive
// together.
func (l *receiverLog) take(line *pendingLine) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	for index, held := range l.pending {
		if held == line {
			l.pending = append(l.pending[:index], l.pending[index+1:]...)
			return true
		}
	}
	return false
}

// fresh answers whether value differs from the last value logged under
// key, and records it. A declared block that the receiver has not
// confirmed goes out again on every pass, and only the first send of
// each value is a line.
func (l *receiverLog) fresh(key string, value any) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if last, held := l.said[key]; held && reflect.DeepEqual(last, value) {
		return false
	}
	l.said[key] = value
	return true
}

// elapsed writes a duration the way a person reads it: milliseconds
// under a second, tenths of a second under ten seconds, and whole
// seconds above that.
func elapsed(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	case d < 10*time.Second:
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + " s"
	}
	return fmt.Sprintf("%d s", int(d.Round(time.Second).Seconds()))
}

// declared writes a declared block as compact JSON with its empty
// objects left out, so a line names only the values a person declared.
// JSON sorts the keys of a map, so one block always reads the same.
// Each block is a spec type or a bus message that was decoded from
// JSON, so it always encodes again, and the errors are dropped.
func declared(block any) string {
	raw, _ := json.Marshal(block)
	var tree any
	_ = json.Unmarshal(raw, &tree)
	pruned, _ := json.Marshal(prune(tree))
	return string(pruned)
}

// prune removes the empty objects from a decoded JSON tree.
func prune(tree any) any {
	object, isObject := tree.(map[string]any)
	if !isObject {
		return tree
	}
	kept := map[string]any{}
	for key, value := range object {
		value = prune(value)
		if inner, isObject := value.(map[string]any); isObject && len(inner) == 0 {
			continue
		}
		kept[key] = value
	}
	return kept
}

// mainZoneCheck answers a check that reads fields of the main zone: each of
// words writes one field as a line states it, and the check passes when
// the fields, joined, equal want.
func mainZoneCheck(driver equipment.Driver, want string, words ...func(equipment.ZoneState, int) string) func() (string, bool) {
	return func() (string, bool) {
		zone := mainZone(driver.State())
		fields := make([]string, 0, len(words))
		for _, write := range words {
			fields = append(fields, write(zone, driver.VolumeResolution()))
		}
		got := strings.Join(fields, " and ")
		return got, got == want
	}
}

// The words a line uses for each field of a zone.
func powerWords(zone equipment.ZoneState, _ int) string {
	if zone.Power == "" {
		return "no power"
	}
	return "power " + string(zone.Power)
}

func inputWords(zone equipment.ZoneState, _ int) string {
	if zone.Input == "" {
		return "no input"
	}
	return "input " + zone.Input
}

func volumeWords(zone equipment.ZoneState, resolution int) string {
	if zone.Volume == equipment.Unknown {
		return "no volume"
	}
	return "volume " + formatSteps(zone.Volume, resolution)
}

func muteWords(zone equipment.ZoneState, _ int) string {
	return "mute " + onOff(zone.Mute)
}

// onOff writes a switch as a line states it.
func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// commandedPower is the power a SetPower of on puts a zone at, in the
// words the driver reports: a Denon reports standby for both standby
// and off.
func commandedPower(on bool) equipment.Power {
	if on {
		return equipment.PowerOn
	}
	return equipment.PowerStandby
}
