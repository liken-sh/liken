// The transcripts in testdata/: the bytes that a real indiserver sent,
// with one simulator behind it, in reply to one message each:
// getProperties, then CONNECTION.CONNECT=On, the simulator's own steps,
// and CONNECTION.DISCONNECT=On. They were captured from the simulators
// of the pinned indi-simulators tag, in the topology of plan 03. The
// replay server in replay_test.go serves them back, so the tests parse
// what the simulators send, not what a test author expects them to
// send.

package indi

import (
	"fmt"
	"regexp"
	"testing"
)

// simulators are the drivers that observatory-operator runs, without
// their indi_simulator_ prefix. Plan 06 gives a kind to each.
var simulators = []string{
	"telescope", "ccd", "guide", "wheel", "focus", "rotator", "dome",
	"weather", "dustcover", "lightpanel", "gps", "pac", "sqm", "io",
	"receiver",
}

// A phase is one message that the capture sent and the file that holds
// the server's reply. Every simulator's transcripts have the phases
// baseline, connect, its own steps, then disconnect.
type phase struct {
	name    string
	message string
}

func phases(simulator, device string) []phase {
	all := []phase{
		{"baseline", `<getProperties version="1.7"/>`},
		{"connect", switchMessage(device, "CONNECTION", "CONNECT")},
	}
	all = append(all, steps(device)[simulator]...)
	return append(all, phase{"disconnect", switchMessage(device, "CONNECTION", "DISCONNECT")})
}

// steps are the changes captured between connect and disconnect. The
// focuser moves within its limits and then past them, which the driver
// refuses with Alert. The dome turns, which takes it through Busy.
func steps(device string) map[string][]phase {
	return map[string][]phase{
		"focus": {
			{"move", numberMessage(device, "ABS_FOCUS_POSITION", "FOCUS_ABSOLUTE_POSITION", "52000")},
			{"refuse", numberMessage(device, "ABS_FOCUS_POSITION", "FOCUS_ABSOLUTE_POSITION", "200000")},
		},
		"dome": {
			{"move", numberMessage(device, "ABS_DOME_POSITION", "DOME_ABSOLUTE_POSITION", "30")},
		},
	}
}

func switchMessage(device, property, member string) string {
	return fmt.Sprintf(`<newSwitchVector device=%q name=%q><oneSwitch name=%q>On</oneSwitch></newSwitchVector>`, device, property, member)
}

func numberMessage(device, property, member, value string) string {
	return fmt.Sprintf(`<newNumberVector device=%q name=%q><oneNumber name=%q>%s</oneNumber></newNumberVector>`, device, property, member, value)
}

var deviceAttribute = regexp.MustCompile(`device="([^"]+)"`)

func firstDevice(t *testing.T, data []byte) string {
	t.Helper()
	match := deviceAttribute.FindSubmatch(data)
	if match == nil {
		t.Fatalf("the baseline names no device: %q", data)
	}
	return string(match[1])
}
