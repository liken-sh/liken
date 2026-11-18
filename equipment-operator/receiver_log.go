package main

// The log lines that let a person follow one receiver: each command the
// operator sends, why it sent it, and what the receiver reported
// afterward. A command's line is held back until the receiver reports
// the result, so one line covers the whole operation. Polls, status
// writes, and events that the receiver sends on its own are not logged
// here.

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// receiverConfirmWait is how long a command's line waits for the
// receiver to report the result. The wiim driver reads some settings,
// such as the input, only at its next poll, so the wait is longer than
// wiim's pollInterval.
const receiverConfirmWait = 15 * time.Second

// receiverLog writes the lines for one receiver. Each line starts with
// the Receiver's name, like the lines of a CECBus.
type receiverLog struct {
	to   io.Writer
	name string

	mutex sync.Mutex
	// pending holds the commands whose lines wait for a report.
	pending []*pendingLine
	// said holds the last value logged under each key, so a command that
	// every pass sends again with the same value is logged once.
	said map[string]any
}

func newReceiverLog(to io.Writer, name string) *receiverLog {
	return &receiverLog{to: to, name: name, said: map[string]any{}}
}

// pendingLine is a command's line while it waits. check returns what
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

// refused writes the line for a command that the driver or the receiver
// refused, with the error text.
func (l *receiverLog) refused(line string, err error) {
	l.printf("%s; the command was not applied: %v", line, err)
}

// confirm writes line when check reports the result, or when
// receiverConfirmWait ends first. began is when the command was sent,
// so the line can say how long the receiver took. The line is added to
// pending before the first check, so a report that arrives between the
// command and this call still completes it.
func (l *receiverLog) confirm(line string, began time.Time, check func() (string, bool)) {
	waiting := &pendingLine{line: line, began: began, check: check}
	l.mutex.Lock()
	waiting.timer = time.AfterFunc(receiverConfirmWait, func() { l.expire(waiting) })
	l.pending = append(l.pending, waiting)
	l.mutex.Unlock()
	l.observe()
}

// observe checks every pending line against what the receiver reports
// now. The receiver unit calls it for each event the receiver sends.
// The checks run outside the mutex because each one reads the driver's
// state.
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

// expire writes the line of a command whose report did not arrive in
// time.
func (l *receiverLog) expire(line *pendingLine) {
	if !l.take(line) {
		return
	}
	report, _ := line.check()
	l.printf("%s; the receiver did not report it in %s, and it reports %s", line.line, elapsed(receiverConfirmWait), report)
}

// take removes a pending line and returns whether this call removed it.
// When a report and the timer arrive together, only one of them gets
// true, so the line is written once.
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

// fresh returns whether value differs from the last value logged under
// key, and records it. A declared block that the receiver has not
// confirmed is sent again on every pass, and only the first send of
// each value should be logged.
func (l *receiverLog) fresh(key string, value any) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if last, held := l.said[key]; held && reflect.DeepEqual(last, value) {
		return false
	}
	l.said[key] = value
	return true
}

// elapsed formats a duration for a log line.
func elapsed(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	case d < 10*time.Second:
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + " s"
	}
	return fmt.Sprintf("%d s", int(d.Round(time.Second).Seconds()))
}

// declared returns a declared block as compact JSON with its empty
// objects removed, so a line shows only the values a person set. JSON
// sorts the keys of a map, so the same block always produces the same
// text. Each block is a spec type or a bus message that was decoded
// from JSON, so it always encodes again, and the errors are ignored.
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

// mainZoneCheck returns a check on the main zone. Each function in
// words formats one field of the zone, and the check passes when the
// formatted fields, joined with " and ", equal want.
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

// soundModeCheck returns a check that passes when the main zone reports
// a sound mode that the driver treats as the same as mode. The driver
// can name one mode with different words.
func soundModeCheck(driver equipment.Driver, mode string) func() (string, bool) {
	return func() (string, bool) {
		zone := mainZone(driver.State())
		return soundModeWords(zone, 0), driver.SameSoundMode(mode, zone.SoundMode)
	}
}

// The functions below format one field of a zone for a line.
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

func soundModeWords(zone equipment.ZoneState, _ int) string {
	if zone.SoundMode == "" {
		return "no sound mode"
	}
	return "sound mode " + zone.SoundMode
}

// wordList joins the formatted fields of a line: two with "and", and
// three or more with commas and a final "and".
func wordList(words []string) string {
	if len(words) < 3 {
		return strings.Join(words, " and ")
	}
	return strings.Join(words[:len(words)-1], ", ") + ", and " + words[len(words)-1]
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

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// commandedPower returns the power that the receiver reports after a
// SetPower with on. It is Standby and never Off when on is false,
// because the denon driver records a zone that is off as Standby.
func commandedPower(on bool) equipment.Power {
	if on {
		return equipment.PowerOn
	}
	return equipment.PowerStandby
}
