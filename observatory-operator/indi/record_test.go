// The recorder of the transcripts in testdata/. Each transcript is the
// bytes that a real indiserver sent, with one simulator behind it, in
// reply to one message: getProperties, then CONNECTION.CONNECT=On, the
// simulator's own steps, and CONNECTION.DISCONNECT=On. The replay
// server in
// replay_test.go serves them back, so the tests parse what the
// simulators send, not what a test author expects them to send.
//
// Record them again after a bump of the indi images:
//
//	go test ./indi -run TestRecordTranscripts -record
//
// The recorder writes raw TCP and parses nothing with the client, so a
// bug in the client cannot shape its own fixtures.

package indi

import (
	"bytes"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

var record = flag.Bool("record", false, "record the transcripts in testdata/ again from the indi-simulators image")

// simulators are the drivers that observatory-operator runs, without
// their indi_simulator_ prefix. Plan 06 gives a kind to each.
var simulators = []string{
	"telescope", "ccd", "guide", "wheel", "focus", "rotator", "dome",
	"weather", "dustcover", "lightpanel", "gps", "pac", "sqm", "io",
	"receiver",
}

// A phase is one message that the recorder sends and the file that
// holds the server's reply. Every simulator's recording has the phases
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

// steps are the changes recorded between connect and disconnect. The
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

// A phase of a recording ends when the server sends nothing for quiet,
// or after most, whichever is first. Both are clocks: a connected
// telescope sends its coordinates on every poll and never goes quiet,
// and a turning dome reports its position until it stops.
const (
	quiet = 1500 * time.Millisecond
	most  = 30 * time.Second
)

func TestRecordTranscripts(t *testing.T) {
	if !*record {
		t.Skip("skipped: run with -record to record the transcripts in testdata/ again")
	}
	image := simulatorImage(t)
	for _, simulator := range simulators {
		t.Run(simulator, func(t *testing.T) {
			recordSimulator(t, image, simulator)
		})
	}
}

func recordSimulator(t *testing.T, image, simulator string) {
	r := startRig(t, image, "indi_simulator_"+simulator)
	conn := dialUntilUp(t, r.address())
	defer conn.Close()

	// The device's name is in the baseline, and the later phases
	// name it, so the baseline is read before the others are built.
	send(t, conn, `<getProperties version="1.7"/>`)
	baseline := readUntilQuiet(t, conn)
	device := firstDevice(t, baseline)
	recorded := map[string][]byte{"baseline": baseline}
	for _, p := range phases(simulator, device)[1:] {
		send(t, conn, p.message)
		recorded[p.name] = readUntilQuiet(t, conn)
	}

	dir := filepath.Join("testdata", simulator)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range recorded {
		if err := os.WriteFile(filepath.Join(dir, name+".xml"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %q, %s: %d bytes", simulator, device, name, len(data))
	}
}

// dialUntilUp dials until the server in a new container listens. Each
// refused dial waits a fixed interval, and the whole wait has a
// deadline.
func dialUntilUp(t *testing.T, address string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err == nil {
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server at %s did not listen: %v", address, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func send(t *testing.T, conn net.Conn, message string) {
	t.Helper()
	if _, err := conn.Write([]byte(message + "\n")); err != nil {
		t.Fatal(err)
	}
}

func switchMessage(device, property, member string) string {
	return fmt.Sprintf(`<newSwitchVector device=%q name=%q><oneSwitch name=%q>On</oneSwitch></newSwitchVector>`, device, property, member)
}

func numberMessage(device, property, member, value string) string {
	return fmt.Sprintf(`<newNumberVector device=%q name=%q><oneNumber name=%q>%s</oneNumber></newNumberVector>`, device, property, member, value)
}

func readUntilQuiet(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	var data []byte
	buffer := make([]byte, 64*1024)
	end := time.Now().Add(most)
	for {
		conn.SetReadDeadline(time.Now().Add(quiet))
		n, err := conn.Read(buffer)
		data = append(data, buffer[:n]...)
		var timeout net.Error
		quietNow := errors.As(err, &timeout) && timeout.Timeout()
		if err != nil && !quietNow {
			t.Fatal(err)
		}
		// A phase ends only between two elements, so each transcript
		// parses on its own.
		if (quietNow || time.Now().After(end)) && whole(data) {
			return data
		}
	}
}

// whole reports whether data ends between two top-level elements.
func whole(data []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	for {
		token, err := decoder.RawToken()
		if err == io.EOF {
			return depth == 0
		}
		if err != nil {
			return false
		}
		switch token.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
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
