package indi

import (
	"slices"
	"sort"
)

// Store holds what one connection reported: every device, and every
// property that each device defined and did not delete. A Client
// updates it from the server's messages, and a caller reads it through
// the Client's methods or inside a WaitFor condition.
//
// The store describes the current connection only. When a connection
// ends, the store empties, because nothing it held is current any
// more: a server that restarted has drivers that restarted too, with
// their settings lost. The next connection fills it from a new
// baseline.
type Store struct {
	connected bool
	// connection counts the connections, so a wait that began on one
	// connection fails when another one replaced it.
	connection uint64
	// sequence counts every change that the store applied. Each
	// property records the sequence of its definition and of its last
	// update, so a wait can tell an update after a send from one
	// before it.
	sequence uint64
	devices  map[string]*device
}

// A device holds its properties in the order the driver defined them,
// the order in which a client such as KStars shows them.
type device struct {
	order      []string
	properties map[string]*entry
}

// An entry records the sequence of the property's first definition,
// of its last change, and of its last change to Busy. Settle reads
// them, because several changes can arrive between two of its checks.
type entry struct {
	property Property
	defined  uint64
	updated  uint64
	busy     uint64
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

// reset empties the store at the start or the end of a connection.
func (s *Store) reset(connected bool) {
	s.connected = connected
	if connected {
		s.connection++
	}
	s.devices = nil
}

// define records a definition. A property that the device defines
// again keeps its place and its identity: only a deletion ends a
// property, so a wait on it continues across a second definition.
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
	e.set(p, s.sequence)
}

// update records a change to a defined property.
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

// removeDevice deletes every property of a device.
func (s *Store) removeDevice(device string) {
	s.sequence++
	delete(s.devices, device)
}

// remove deletes one property.
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
