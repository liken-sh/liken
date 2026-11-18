// Package capture records what a real indiserver sends with one driver
// behind it. It writes the transcripts of the simulators in
// indi/inditest/testdata/, which the replay tests serve, and the
// captures of the bare drivers in indi/standard/, which hold the
// standard properties of each INDI interface.
//
// A capture sends raw XML and does not parse the replies with the indi
// package's client, so a bug in that client cannot change the fixtures
// that its own tests use.
package capture

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"time"
)

// A phase of a capture ends at the first boundary between top-level
// elements after the server sends nothing for Quiet, or after Most.
// Most is needed because a connected telescope reports its coordinates
// on every poll and never goes quiet. Quiet is longer than the slowest
// reply of a simulator: the CCD simulator waits 1.5 to 2 seconds before
// it reports a new filter slot.
const (
	Quiet = 3 * time.Second
	Most  = 30 * time.Second
)

// Baseline is the first message of every capture. The reply names the
// device, and the later messages address it by that name.
const Baseline = `<getProperties version="1.7"/>`

// A Phase is one message that a capture sends, and the name under which
// the server's reply to it is recorded.
type Phase struct {
	Name    string
	Message string
}

// A Script is what a capture sends to one device. Setup holds messages
// that the server does not reply to, such as an enableBLOB. Run sends
// Baseline itself and records its reply under the name of Phases[0],
// so the Message of Phases[0] is not sent. Then it sends Setup, and
// then each later phase.
type Script struct {
	Setup  []string
	Phases []Phase
}

// A Reply is the bytes that the server sent in response to one phase.
type Reply struct {
	Phase string
	Data  []byte
}

// Run sends the baseline, reads the device's name from the reply, and
// then sends the script for that device, one phase at a time. Each
// reply ends between two top-level elements, so it parses on its own.
func Run(conn net.Conn, script func(device string) Script) (string, []Reply, error) {
	if err := send(conn, Baseline); err != nil {
		return "", nil, err
	}
	baseline, err := readUntilQuiet(conn)
	if err != nil {
		return "", nil, err
	}
	device, err := firstDevice(baseline)
	if err != nil {
		return "", nil, err
	}
	s := script(device)
	replies := []Reply{{s.Phases[0].Name, baseline}}
	for _, message := range s.Setup {
		if err := send(conn, message); err != nil {
			return "", nil, err
		}
	}
	for _, p := range s.Phases[1:] {
		if err := send(conn, p.Message); err != nil {
			return "", nil, err
		}
		data, err := readUntilQuiet(conn)
		if err != nil {
			return "", nil, err
		}
		replies = append(replies, Reply{p.Name, data})
	}
	return device, replies, nil
}

func send(conn net.Conn, message string) error {
	_, err := conn.Write([]byte(message + "\n"))
	return err
}

func readUntilQuiet(conn net.Conn) ([]byte, error) {
	var data []byte
	buffer := make([]byte, 64*1024)
	end := time.Now().Add(Most)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(Quiet)); err != nil {
			return nil, err
		}
		n, err := conn.Read(buffer)
		data = append(data, buffer[:n]...)
		var timeout net.Error
		quiet := errors.As(err, &timeout) && timeout.Timeout()
		if err != nil && !quiet {
			return nil, err
		}
		if (quiet || time.Now().After(end)) && whole(data) {
			return data, nil
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

func firstDevice(data []byte) (string, error) {
	match := deviceAttribute.FindSubmatch(data)
	if match == nil {
		return "", fmt.Errorf("the baseline has no device attribute: %q", data)
	}
	return string(match[1]), nil
}

func switchMessage(device, property, member string) string {
	return fmt.Sprintf(`<newSwitchVector device=%q name=%q><oneSwitch name=%q>On</oneSwitch></newSwitchVector>`, device, property, member)
}

func numberMessage(device, property, member, value string) string {
	return fmt.Sprintf(`<newNumberVector device=%q name=%q><oneNumber name=%q>%s</oneNumber></newNumberVector>`, device, property, member, value)
}
