package indi

import (
	"slices"
	"time"
)

// Type is the kind of value that a property's members hold. Each type
// has its own def, set, and new elements on the wire, such as
// defNumberVector, setNumberVector, and newNumberVector.
type Type string

const (
	NumberType Type = "Number"
	SwitchType Type = "Switch"
	TextType   Type = "Text"
	LightType  Type = "Light"
	BLOBType   Type = "BLOB"
)

// Permission says whether a client can write a property. A Light
// property has no permission attribute on the wire, and the store
// records it as ReadOnly, because no client can write a light.
type Permission string

const (
	ReadOnly  Permission = "ro"
	WriteOnly Permission = "wo"
	ReadWrite Permission = "rw"
)

// State is a property's state, and also the value of each member of a
// Light property. A driver sets Busy while it carries out a change, and
// Ok or Alert when the change ends.
type State string

const (
	Idle  State = "Idle"
	Ok    State = "Ok"
	Busy  State = "Busy"
	Alert State = "Alert"
)

// Rule says how many members of a Switch property can be On at once.
type Rule string

const (
	// OneOfMany means exactly one member is On.
	OneOfMany Rule = "OneOfMany"
	// AtMostOne means one member or none is On.
	AtMostOne Rule = "AtMostOne"
	// AnyOfMany puts no limit on the members that are On.
	AnyOfMany Rule = "AnyOfMany"
)

// Property is one property of one device, as the device defined it and
// as its later updates changed it. The store hands out copies, so a
// caller can keep one and the next update does not change it.
type Property struct {
	Device string
	Name   string
	Label  string
	Group  string
	Type   Type
	Perm   Permission
	State  State
	// Rule is set on a Switch property only.
	Rule Rule
	// Timeout is how long the driver expects a change to take, as the
	// driver states it. Zero means the driver states no limit.
	Timeout time.Duration
	// Timestamp is the time that the driver gave its last definition or
	// update, in UTC. It is zero when the driver gave none.
	Timestamp time.Time
	Members   []Member
}

// Member is one element of a property. The fields that apply depend on
// the property's Type, and the others are zero.
type Member struct {
	Name  string
	Label string

	// Number is the value of a Number member. Format is the member's
	// printf format, such as "%.2f", or a sexagesimal format, such as
	// "%010.6m", which FormatNumber prints. Min, Max, and Step are the
	// limits that the driver states, and Step is zero when the driver
	// states no step.
	Number float64
	Format string
	Min    float64
	Max    float64
	Step   float64

	// Switch is true when a Switch member is On.
	Switch bool

	// Text is the value of a Text member.
	Text string

	// Light is the value of a Light member.
	Light State

	// BLOBFormat and BLOBSize describe the last BLOB that the client
	// received for a BLOB member, such as ".fits" and its size in bytes
	// before encoding. The store keeps no BLOB data: Event.BLOBs
	// carries the data to subscribers, and only to them.
	BLOBFormat string
	BLOBSize   int
}

// Member returns the member of the given name.
func (p Property) Member(name string) (Member, bool) {
	for _, member := range p.Members {
		if member.Name == name {
			return member, true
		}
	}
	return Member{}, false
}

// On lists the names of the Switch members that are On, in the order
// the device defined them.
func (p Property) On() []string {
	var on []string
	for _, member := range p.Members {
		if member.Switch {
			on = append(on, member.Name)
		}
	}
	return on
}

// clone copies the members, so a copy of the property shares no memory
// with the store.
func (p Property) clone() Property {
	p.Members = slices.Clone(p.Members)
	return p
}
