// Package inditest is a fake indiserver for tests, built from the bytes
// that the real simulators sent, which testdata/ holds. A Server runs
// one Driver for each simulator it names. A driver defines what its
// baseline transcript defines, adds what its connect transcript
// defines when a client connects it, and answers every other change
// with the values sent and the state Ok. A test can hold a property,
// so its driver never answers, refuse it, so its driver answers Alert,
// or make it move, so its driver answers Busy until the test sets its
// state.
//
// A client dials the Server through its DialContext, and each dial is
// one end of a net.Pipe, so a test runs in a testing/synctest bubble on
// the fake clock, which a real socket stops. The package uses the
// standard library alone, so another component's tests take it with
// no more of observatory-operator than the package.
package inditest

import (
	"context"
	"encoding/xml"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// Server is one fake indiserver and the drivers it runs.
type Server struct {
	mu      sync.Mutex
	drivers []*Driver
	conns   []*Conn
	closed  bool
	// sent records each change that a client sent.
	sent    []string
	held    map[string]bool
	refused map[string]bool
	moving  map[string]bool
}

// NewServer answers a server that runs one driver for each simulator,
// named as a directory of testdata/ is, such as "telescope" and "ccd",
// or as the INDI driver is, such as "indi_simulator_ccd". It closes
// when the test ends.
func NewServer(t testing.TB, simulators ...string) *Server {
	t.Helper()
	s := &Server{held: map[string]bool{}, refused: map[string]bool{}, moving: map[string]bool{}}
	for _, simulator := range simulators {
		d, err := NewDriver(trimmed(simulator))
		if err != nil {
			t.Fatal(err)
		}
		s.drivers = append(s.drivers, d)
	}
	t.Cleanup(s.Close)
	return s
}

// DialContext answers one end of a new pipe to the server, and serves
// the other, whatever the address. A closed server refuses the dial
// with ECONNREFUSED, as a host with no listener on the port does.
func (s *Server) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, &net.OpError{Op: "dial", Net: network, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	}
	client, server := net.Pipe()
	c := NewConn(server)
	s.conns = append(s.conns, c)
	go s.serve(c)
	return client, nil
}

// serve reads one client's messages until the connection closes.
func (s *Server) serve(c *Conn) {
	defer func() {
		c.Close()
		s.mu.Lock()
		s.conns = slices.DeleteFunc(s.conns, func(o *Conn) bool { return o == c })
		s.mu.Unlock()
	}()
	decoder := xml.NewDecoder(c)
	for {
		token, err := decoder.Token()
		if err != nil {
			return
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		var v Vector
		if err := decoder.DecodeElement(&v, &start); err != nil {
			return
		}
		s.answer(c, v, start)
	}
}

func (s *Server) answer(c *Conn, v Vector, start xml.StartElement) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tag := v.XMLName.Local
	switch {
	case tag == "getProperties":
		for _, d := range s.drivers {
			if v.Device != "" && d.Device != v.Device {
				continue
			}
			for _, p := range d.Props {
				c.Send(p.Def())
			}
		}
		return
	case tag == "pingRequest":
		for _, a := range start.Attr {
			if a.Name.Local == "uid" {
				c.Send(`<pingReply uid="` + Escape(a.Value) + `"/>` + "\n")
			}
		}
		return
	case !strings.HasPrefix(tag, "new"):
		return
	}
	d := s.driver(v.Device)
	if d == nil || d.Find(v.Name) == nil {
		return
	}
	k := key(v.Device, v.Name)
	s.sent = append(s.sent, strings.TrimSpace(k+" "+strings.Join(v.Changes(), " ")))
	p := d.Find(v.Name)
	switch {
	case s.held[k]:
		return
	case s.refused[k]:
		p.State = "Alert"
		s.broadcast(p.Set())
		return
	case s.moving[k] && p.State == "Busy":
		Interrupt(p)
		s.broadcast(p.Set())
		return
	}
	d.Apply(v)
	if s.moving[k] {
		p.State = "Busy"
	}
	s.broadcast(p.Set())
	for _, ended := range d.Abort(p) {
		s.broadcast(ended.Set())
	}
	if p.Name != "CONNECTION" {
		return
	}
	if Connecting(p) {
		for _, added := range d.Connect(nil) {
			s.broadcast(added.Def())
		}
		return
	}
	for _, name := range d.Disconnect() {
		s.broadcast(fmt.Sprintf("<delProperty device=\"%s\" name=\"%s\"/>\n", Escape(d.Device), Escape(name)))
	}
}

// driver answers the driver of a device, or nil. The caller holds mu.
func (s *Server) driver(device string) *Driver {
	for _, d := range s.drivers {
		if d.Device == device {
			return d
		}
	}
	return nil
}

// broadcast sends text to every client. The caller holds mu.
func (s *Server) broadcast(text string) {
	for _, c := range s.conns {
		c.Send(text)
	}
}

// Hold makes a driver never answer a change to one property, as a
// driver whose hardware stops answering does.
func (s *Server) Hold(device, property string) { s.mark(s.held, device, property, true) }

// Release makes a held property's driver answer again. A change that
// it did not answer stays unanswered, as a real driver's would.
func (s *Server) Release(device, property string) { s.mark(s.held, device, property, false) }

// Refuse makes a driver answer every change to one property with
// Alert and keep its values, as a driver whose hardware fails does.
func (s *Server) Refuse(device, property string) { s.mark(s.refused, device, property, true) }

// Accept makes a driver that refused a property answer it again.
func (s *Server) Accept(device, property string) { s.mark(s.refused, device, property, false) }

// Move makes a driver answer each change to one property with the
// values sent and the state Busy, as a mount that slews or a camera
// that exposes does. The motion ends when the test sets the state with
// SetState, and a change during the motion interrupts it with Alert.
func (s *Server) Move(device, property string) { s.mark(s.moving, device, property, true) }

func (s *Server) mark(set map[string]bool, device, property string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		set[key(device, property)] = true
	} else {
		delete(set, key(device, property))
	}
}

// SetState sets a property's state and reports it, as a driver does
// when an exposure or a slew ends.
func (s *Server) SetState(device, property, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.find(device, property); p != nil {
		p.State = state
		s.broadcast(p.Set())
	}
}

// Report sets one member of a property and reports it, as a driver
// reports a reading.
func (s *Server) Report(device, property, member, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.find(device, property); p != nil {
		p.SetValue(member, value)
		s.broadcast(p.Set())
	}
}

// Message sends a device's message to every client, as a driver does
// when it saves a frame.
func (s *Server) Message(device, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcast(fmt.Sprintf("<message device=\"%s\" message=\"%s\"/>\n", Escape(device), Escape(text)))
}

// find answers a device's property, or nil. The caller holds mu.
func (s *Server) find(device, property string) *Property {
	if d := s.driver(device); d != nil {
		return d.Find(property)
	}
	return nil
}

// Property answers a copy of a device's property as its driver holds
// it now, or nil when the driver does not define it.
func (s *Server) Property(device, property string) *Property {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.find(device, property); p != nil {
		return p.Clone()
	}
	return nil
}

// Changes answers the changes that clients sent, in order, as
// "<device>.<property> <member>=<value> ...".
func (s *Server) Changes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
}

// Connected answers the devices that a client has connected, in order
// of their names.
func (s *Server) Connected() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, d := range s.drivers {
		if d.Connected {
			out = append(out, d.Device)
		}
	}
	slices.Sort(out)
	return out
}

// Clients counts the open client connections.
func (s *Server) Clients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// Cut closes every client connection while the drivers run, as a
// network that drops a connection does.
func (s *Server) Cut() {
	s.mu.Lock()
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}

// Close stops the server: it closes every connection and refuses each
// later dial.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.Cut()
}
