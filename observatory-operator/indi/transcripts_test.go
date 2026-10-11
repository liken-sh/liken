// The transcripts in testdata/: the bytes that a real indiserver sent,
// with one simulator behind it, in reply to one message each:
// getProperties, then CONNECTION.CONNECT=On, the simulator's own steps,
// and CONNECTION.DISCONNECT=On. capture.SimulatorScript holds the
// messages, and `make transcripts` records them again from the
// simulators of the pinned indi-simulators tag, each driver in the
// server's own container. The replay server in replay_test.go serves
// them back, so the tests parse what the simulators send, not what a
// test author expects them to send.

package indi

import (
	"regexp"
	"testing"

	"github.com/liken-sh/liken/observatory-operator/indi/capture"
)

// simulators are the drivers that observatory-operator runs, without
// their indi_simulator_ prefix.
var simulators = capture.Simulators

var deviceAttribute = regexp.MustCompile(`device="([^"]+)"`)

func firstDevice(t *testing.T, data []byte) string {
	t.Helper()
	match := deviceAttribute.FindSubmatch(data)
	if match == nil {
		t.Fatalf("the baseline names no device: %q", data)
	}
	return string(match[1])
}
