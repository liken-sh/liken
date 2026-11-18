package main

// The fake receiver that the controller tests run against. It keeps
// power, volume, mute, input, and sound mode, answers their queries as
// an AVR-X1700H did, and can change its own volume or mute or drop the
// connection, as a person at the receiver or a network fault would.

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
)

// denonNoise holds lines that an AVR-X1700H sent unprompted after the
// MS? query, so the tests receive the same unrequested lines.
var denonNoise = []string{"SVOFF", "PSDRC OFF", "PSLFE 00"}

type fakeDenon struct {
	listening string
	commands  chan string

	mutex       sync.Mutex
	power       string
	volume      int
	volumeMax   int
	mute        bool
	input       string
	soundMode   string
	ignorePower bool
	// settingsHold maps a set command for a setting or a zone to the
	// line the receiver reports for it, which models a receiver that
	// accepts the command without applying it. A setting or zone
	// command not in the map is never reported, which models a control
	// that the receiver never answers.
	settingsHold map[string]string
	// holdingEchoes makes the receiver apply each volume it is sent but
	// report none of them, which models a receiver that falls behind the
	// volume commands it receives.
	holdingEchoes bool
	conns         []net.Conn
}

// startFakeDenon starts a fake receiver that listens on the test network
// until the test ends.
func startFakeDenon(t *testing.T) *fakeDenon {
	t.Helper()
	receiver := &fakeDenon{
		commands:  make(chan string, 64),
		power:     "PWSTANDBY",
		volume:    100,
		volumeMax: 139,
		input:     "MPLAY",
		soundMode: "MULTI CH IN",
	}
	receiver.listening = testNetwork.listen(t, receiver.serve)
	t.Cleanup(receiver.dropConnections)
	return receiver
}

func (f *fakeDenon) address() string {
	return f.listening
}

// alias returns a second address that reaches the same receiver, as a
// host name and an IP address in a Receiver's spec can both name one
// device.
func (f *fakeDenon) alias(t *testing.T) string {
	t.Helper()
	return testNetwork.listen(t, f.serve)
}

func (f *fakeDenon) serve(conn net.Conn) {
	f.mutex.Lock()
	f.conns = append(f.conns, conn)
	f.mutex.Unlock()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\r')
		if err != nil {
			return
		}
		command := strings.Trim(line, "\r\n")
		if command == "" {
			continue
		}
		select {
		case f.commands <- command:
		default:
		}
		f.answer(command)
	}
}

// answer applies one command to the receiver's state and sends the lines
// a real receiver would send back to every connected client.
func (f *fakeDenon) answer(command string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	switch {
	case command == "PW?":
		f.send(f.power)
	case command == "MV?":
		f.send("MV" + denon.HalfStepDigits(f.volume))
		f.send("MVMAX " + denon.HalfStepDigits(f.volumeMax))
	case command == "MU?":
		f.send(f.muteLine())
	case command == "SI?":
		f.send("SI" + f.input)
	case command == "MS?":
		f.send("MS" + f.soundMode)
		for _, line := range denonNoise {
			f.send(line)
		}
	case command == "PWON", command == "PWSTANDBY":
		f.power = command
		if !f.ignorePower {
			f.send(f.power)
		}
	case command == "MUON", command == "MUOFF":
		f.mute = command == "MUON"
		f.send(f.muteLine())
	case strings.HasPrefix(command, "MV"):
		halves, ok := denon.ParseHalfSteps(command[2:])
		if !ok {
			return
		}
		f.volume = halves
		if f.holdingEchoes {
			return
		}
		f.send("MV" + denon.HalfStepDigits(f.volume))
	case strings.HasPrefix(command, "SI"):
		f.input = command[2:]
		f.send("SI" + f.input)
	case strings.HasPrefix(command, "MS") && !strings.HasPrefix(command, "MSQUICK"):
		f.soundMode = command[2:]
		f.send("MS" + f.soundMode)
	case strings.HasPrefix(command, "Z2"), strings.HasPrefix(command, "Z3"):
		if report, held := f.settingsHold[command]; held {
			f.send(report)
		}
	case isDenonSetting(command):
		if report, held := f.settingsHold[command]; held {
			f.send(report)
		}
	}
}

func (f *fakeDenon) muteLine() string {
	if f.mute {
		return "MUON"
	}
	return "MUOFF"
}

// send writes one line to every open connection. The caller holds
// f.mutex.
func (f *fakeDenon) send(line string) {
	for _, conn := range f.conns {
		conn.SetWriteDeadline(time.Now().Add(testTimeout))
		conn.Write([]byte(line + "\r"))
	}
}

// turnKnob changes the volume as if a person turned the receiver's
// knob. The receiver reports the new volume without being asked.
func (f *fakeDenon) turnKnob(halves int) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.volume = halves
	f.send("MV" + denon.HalfStepDigits(f.volume))
}

// setMute changes the mute state as if a person pressed the receiver's
// mute button, and reports it without being asked.
func (f *fakeDenon) setMute(muted bool) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.mute = muted
	f.send(f.muteLine())
}

var denonSettingPrefixes = []string{
	"ECO", "DIM ", "STBY", "SPPR ", "SD", "SV", "BTTX ", "PSMULTEQ:", "PSDYNEQ ",
	"PSREFLEV ", "PSDYNVOL ", "PSLOM ", "PSDRC ", "PSLFE ", "PSEFF ", "PSDELAY ",
	"PSDEL ", "PSSWR ", "PSRSTR ", "PSGEQ ", "PSHEQ ", "PSSPV ", "PSDEH ",
	"PSBAS ", "PSTRE ", "PSTONE CTRL ", "CV", "VSAUDIO ", "SSHOS",
}

// isDenonSetting reports whether command sets a setting. A query ends
// in ?.
func isDenonSetting(command string) bool {
	if strings.HasSuffix(command, "?") {
		return false
	}
	for _, prefix := range denonSettingPrefixes {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}

// holdSetting makes the receiver report report in response to command.
// A report that matches the value the command sets models the receiver
// applying it.
func (f *fakeDenon) holdSetting(command, report string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.settingsHold == nil {
		f.settingsHold = map[string]string{}
	}
	f.settingsHold[command] = report
}

// volunteer sends each line to every client unasked, as if a person
// changed the receiver with its own controls.
func (f *fakeDenon) volunteer(lines ...string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	for _, line := range lines {
		f.send(line)
	}
}

// ignorePowerOn makes the receiver apply PWON and never report it, so
// a session's wait for power-on times out.
func (f *fakeDenon) ignorePowerOn() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.ignorePower = true
}

// driftLimit makes the receiver report a new MVMAX. A real AVR-X1700H
// has reported different MVMAX values over time, so the maximum volume
// is not a fixed number that the operator can rely on.
func (f *fakeDenon) driftLimit(halves int) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.volumeMax = halves
	f.send("MVMAX " + denon.HalfStepDigits(f.volumeMax))
}

// dropConnections closes every open connection, as a router reboot
// would.
func (f *fakeDenon) dropConnections() {
	f.mutex.Lock()
	conns := f.conns
	f.conns = nil
	f.mutex.Unlock()
	for _, conn := range conns {
		conn.Close()
	}
}

// waitForCommand returns the next command the client sent. It fails the
// test after testTimeout if no command arrives.
func (f *fakeDenon) waitForCommand(t *testing.T) string {
	t.Helper()
	select {
	case command := <-f.commands:
		return command
	case <-time.After(testTimeout):
		t.Fatalf("the client sent no command within %v", testTimeout)
		return ""
	}
}

// refuseCommand fails the test if the client sends unwanted within the
// given duration.
func (f *fakeDenon) refuseCommand(t *testing.T, unwanted string, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case command := <-f.commands:
			if command == unwanted {
				t.Fatalf("the client sent %q again within %v, want it to stay silent", unwanted, within)
			}
		case <-deadline:
			return
		}
	}
}

// refuseAnySet fails the test if the client sends a set command for a
// setting or for zone 2 or 3 within the given duration. Queries and the
// heartbeat end in ?, so they are allowed.
func (f *fakeDenon) refuseAnySet(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case command := <-f.commands:
			if strings.HasSuffix(command, "?") {
				continue
			}
			if isDenonSetting(command) || strings.HasPrefix(command, "Z2") || strings.HasPrefix(command, "Z3") {
				t.Fatalf("the client sent %q within %v, want no set command", command, within)
			}
		case <-deadline:
			return
		}
	}
}

// waitForCommands reads commands until it sees want, and returns every
// command it read, including want.
func (f *fakeDenon) waitForCommands(t *testing.T, want string) []string {
	t.Helper()
	var seen []string
	deadline := time.After(testTimeout)
	for {
		select {
		case command := <-f.commands:
			seen = append(seen, command)
			if command == want {
				return seen
			}
		case <-deadline:
			t.Fatalf("the client sent %v, want %q among them", seen, want)
			return seen
		}
	}
}
