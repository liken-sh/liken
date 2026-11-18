// Package inditest is a fake indiserver for tests. Its drivers replay
// transcripts that were recorded from the real INDI simulators, which
// testdata/ holds. A Server runs one Driver for each simulator it is
// given. A driver defines the properties of its baseline transcript,
// adds those of its connect transcript when a client connects the
// device, and answers every other change by setting the values sent
// and the state Ok. A test can change that for one property. Hold makes
// its driver never answer, Refuse makes it answer Alert, and Move makes
// it answer Busy until the test sets the state.
//
// A client dials the Server through DialContext, which returns one end
// of a net.Pipe. Unlike a real socket, a pipe lets a test run in a
// testing/synctest bubble on the fake clock. The package imports only
// the standard library, so tests of other components can use it without
// importing the rest of observatory-operator.
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

// NewServer returns a server that runs one driver for each simulator. A
// simulator is named either as a directory of testdata/, such as
// "telescope" and "ccd", or as its INDI driver, such as
// "indi_simulator_ccd". The server closes when the test ends.
func NewServer(t testing.TB, simulators ...string) *Server {
	t.Helper()
	s := &Server{held: map[string]bool{}, refused: map[string]bool{}, moving: map[string]bool{}}
	for _, simulator := range simulators {
		d, err := NewDriver(trimmed(simulator))
		if err != nil {
			t.Fatalf("loading the simulator %q: %v", simulator, err)
		}
		s.drivers = append(s.drivers, d)
	}
	t.Cleanup(s.Close)
	return s
}

// DialContext returns one end of a new pipe and serves the other end,
// whatever the address. After Close it fails with ECONNREFUSED, as a
// host with nothing listening on the port does.
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
		s.respond(c, v, start)
	}
}

// respond handles one message from a client: it sends definitions for
// getProperties, replies to pingRequest, and applies each new* change
// to the driver's property.
func (s *Server) respond(c *Conn, v Vector, start xml.StartElement) {
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

// driver returns the driver of a device, or nil. The caller holds mu.
func (s *Server) driver(device string) *Driver {
	for _, d := range s.drivers {
		if d.Device == device {
			return d
		}
	}
	return nil
}

// broadcast sends text to every connected client. The caller holds mu.
func (s *Server) broadcast(text string) {
	for _, c := range s.conns {
		c.Send(text)
	}
}

// Hold makes the driver never answer a change to the property, as when
// its hardware stops responding.
func (s *Server) Hold(device, property string) { s.mark(s.held, device, property, true) }

// Release makes the driver answer changes to the property again. A
// change that it did not answer stays unanswered, as with a real
// driver.
func (s *Server) Release(device, property string) { s.mark(s.held, device, property, false) }

// Refuse makes the driver answer every change to the property with the
// state Alert and keep its values, as when its hardware fails.
func (s *Server) Refuse(device, property string) { s.mark(s.refused, device, property, true) }

// Accept makes a driver that was refusing a property answer it normally
// again.
func (s *Server) Accept(device, property string) { s.mark(s.refused, device, property, false) }

// Move makes the driver answer each change to the property with the
// values sent and the state Busy, as a slewing mount or an exposing
// camera does. The motion ends when the test calls SetState. A change
// during the motion interrupts it: the driver turns every member Off and
// answers Alert.
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

// SetState sets a property's state and sends it to the clients, as a
// driver does when an exposure or a slew ends.
func (s *Server) SetState(device, property, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.find(device, property); p != nil {
		p.State = state
		s.broadcast(p.Set())
	}
}

// Report sets one member of a property and sends the property to the
// clients, as a driver does when it reads a sensor.
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

// find returns a device's property, or nil. The caller holds mu.
func (s *Server) find(device, property string) *Property {
	if d := s.driver(device); d != nil {
		return d.Find(property)
	}
	return nil
}

// Property returns a copy of a device's property as its driver has it
// now, or nil when the driver does not define it.
func (s *Server) Property(device, property string) *Property {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.find(device, property); p != nil {
		return p.Clone()
	}
	return nil
}

// Changes returns the changes that clients sent, in order, each as
// "<device>.<property> <member>=<value> ...".
func (s *Server) Changes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
}

// Connected returns the names of the devices that a client has
// connected, sorted.
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

// Clients returns the number of open client connections.
func (s *Server) Clients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// Cut closes every client connection while the drivers keep running, as
// a network failure does.
func (s *Server) Cut() {
	s.mu.Lock()
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}

// Close stops the server. It closes every connection and refuses every
// later dial.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.Cut()
}
