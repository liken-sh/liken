package indi

import (
	"slices"
	"sort"
)

// Store holds every property that a device defined and has not deleted
// on the current connection. A Client updates it from the server's
// messages, and a caller reads it through the Client's methods or inside
// a WaitFor condition.
//
// The Client empties the store when a connection starts and when it
// ends, because the next connection can be to a restarted server whose
// drivers lost their settings. The server's definitions fill it again.
type Store struct {
	connected bool
	// connection counts the connections, so a wait that started on one
	// connection fails when a later connection replaces it.
	connection uint64
	// sequence counts every change to the store. Each entry records
	// the sequence of its definitions and updates, so a wait can tell an
	// update that came after a send from one that came before it.
	sequence uint64
	devices  map[string]*device
}

// A device keeps its properties in the order the driver first defined
// them, which is the order that a client such as KStars shows.
type device struct {
	order      []string
	properties map[string]*entry
}

// An entry is one property with the store's sequence number at its
// first definition, its last definition, its last change, and its last
// change to Busy. Settle and Sync compare these numbers with the
// sequence at the time of a send, because several changes can arrive
// between two of their checks.
type entry struct {
	property  Property
	defined   uint64
	redefined uint64
	updated   uint64
	busy      uint64
}

// Connected reports whether the client has a connection to the server.
func (s *Store) Connected() bool { return s.connected }

// Devices lists the devices that have at least one property, sorted by
// name.
func (s *Store) Devices() []string {
	var names []string
	for name := range s.devices {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Properties lists a device's properties in the order the driver first
// defined them.
func (s *Store) Properties(device string) []Property {
	d := s.devices[device]
	if d == nil {
		return nil
	}
	properties := make([]Property, 0, len(d.order))
	for _, name := range d.order {
		properties = append(properties, d.properties[name].property.clone())
	}
	return properties
}

// Property returns one property of one device.
func (s *Store) Property(device, name string) (Property, bool) {
	e := s.entry(device, name)
	if e == nil {
		return Property{}, false
	}
	return e.property.clone(), true
}

func (s *Store) entry(device, name string) *entry {
	if d := s.devices[device]; d != nil {
		return d.properties[name]
	}
	return nil
}

func (s *Store) reset(connected bool) {
	s.connected = connected
	if connected {
		s.connection++
	}
	s.devices = nil
}

// define records a definition. A property that the device defines
// again keeps its position and its entry, so a wait on it continues
// across the second definition.
func (s *Store) define(p Property) {
	s.sequence++
	if s.devices == nil {
		s.devices = map[string]*device{}
	}
	d := s.devices[p.Device]
	if d == nil {
		d = &device{properties: map[string]*entry{}}
		s.devices[p.Device] = d
	}
	e := d.properties[p.Name]
	if e == nil {
		e = &entry{defined: s.sequence}
		d.properties[p.Name] = e
		d.order = append(d.order, p.Name)
	}
	e.redefined = s.sequence
	e.set(p, s.sequence)
}

func (s *Store) update(e *entry, p Property) {
	s.sequence++
	e.set(p, s.sequence)
}

func (e *entry) set(p Property, sequence uint64) {
	e.property = p
	e.updated = sequence
	if p.State == Busy {
		e.busy = sequence
	}
}

func (s *Store) removeDevice(device string) {
	s.sequence++
	delete(s.devices, device)
}

func (s *Store) remove(device, name string) {
	s.sequence++
	d := s.devices[device]
	if d == nil || d.properties[name] == nil {
		return
	}
	delete(d.properties, name)
	d.order = slices.DeleteFunc(d.order, func(n string) bool { return n == name })
	if len(d.order) == 0 {
		delete(s.devices, device)
	}
}
