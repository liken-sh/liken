// The transcripts in inditest/testdata/ are the bytes that a real
// indiserver sent, with one simulator driver behind it, in reply to the
// messages of capture.SimulatorScript: getProperties,
// CONNECTION.CONNECT=On, the simulator's own steps, and
// CONNECTION.DISCONNECT=On. `make transcripts` records them again from
// the indi-simulators image of the pinned INDI version, with each driver
// under its own indiserver in its own container. The replay server in
// replay_test.go serves them back, so the tests parse what the
// simulators send and not what a test author expects them to send.

package indi

import (
	"regexp"
	"testing"

	"github.com/liken-sh/liken/observatory-operator/indi/capture"
)

var simulators = capture.Simulators

var deviceAttribute = regexp.MustCompile(`device="([^"]+)"`)

func firstDevice(t *testing.T, data []byte) string {
	t.Helper()
	match := deviceAttribute.FindSubmatch(data)
	if match == nil {
		t.Fatalf("the baseline has no device attribute: %q", data)
	}
	return string(match[1])
}
