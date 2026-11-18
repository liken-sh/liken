package indi

import (
	"slices"
	"time"
)

// Type is the kind of value that a property's members have. Each type
// has its own def, set, and new elements in the INDI protocol, such as
// defNumberVector, setNumberVector, and newNumberVector.
type Type string

const (
	NumberType Type = "Number"
	SwitchType Type = "Switch"
	TextType   Type = "Text"
	LightType  Type = "Light"
	BLOBType   Type = "BLOB"
)

// Permission is whether a client can write a property. A Light
// property has no perm attribute in the protocol, and the store records
// it as ReadOnly.
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

// Rule limits how many members of a Switch property can be On at once.
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
// as later updates changed it. The store returns copies, so a later
// update does not change a Property that a caller kept.
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
	// Timeout is how long the driver says a change can take. Zero means
	// the driver gave no timeout.
	Timeout time.Duration
	// Timestamp is the time that the driver sent with its last
	// definition or update, in UTC. It is zero when the driver sent
	// none.
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
	// limits that the driver defined. Step is zero when the driver
	// defined none.
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
	// received for a BLOB member: its file format, such as ".fits", and
	// its size in bytes before encoding. The store does not keep the
	// BLOB data. Event.BLOBs gives it to subscribers.
	BLOBFormat string
	BLOBSize   int
}

// Member returns the member with the given name, and false when the
// property has none.
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

// clone copies the members, so the copy shares no slice with the
// store.
func (p Property) clone() Property {
	p.Members = slices.Clone(p.Members)
	return p
}
