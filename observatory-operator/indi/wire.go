// The INDI wire protocol, version 1.7. This file decodes the elements
// that a server sends into the store's types and writes the elements
// that a client sends. The protocol is a stream of XML elements with no
// root element, so the reader decodes one top-level element at a time.
// The references are https://docs.indilib.org/protocol/ and the client
// code of libindi, libs/indiabstractclient/ in indilib/indi.

package indi

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// protocolVersion is INDIV in libindi's libs/indicore/indiapi.h. INDI
// 2.x still sends version 1.7 to clients, because its changes are in the
// protocol between the server and its drivers.
const protocolVersion = "1.7"

// element is any top-level element that a server sends. It has a field
// for every attribute of every element type that the client reads, and
// the attributes that an element does not have stay empty.
type element struct {
	XMLName   xml.Name
	Device    string   `xml:"device,attr"`
	Name      string   `xml:"name,attr"`
	Label     string   `xml:"label,attr"`
	Group     string   `xml:"group,attr"`
	State     string   `xml:"state,attr"`
	Perm      string   `xml:"perm,attr"`
	Rule      string   `xml:"rule,attr"`
	Timeout   string   `xml:"timeout,attr"`
	Timestamp string   `xml:"timestamp,attr"`
	Message   string   `xml:"message,attr"`
	UID       string   `xml:"uid,attr"`
	Members   []member `xml:",any"`
	// wholeDevice is true for a delProperty with no name attribute,
	// which deletes the device. An empty name attribute deletes nothing.
	wholeDevice bool
}

// member is one def* or one* child of a vector. A one* element in an
// update has only the attributes that changed, so an empty attribute
// means no change.
type member struct {
	XMLName xml.Name
	Name    string `xml:"name,attr"`
	Label   string `xml:"label,attr"`
	Format  string `xml:"format,attr"`
	Min     string `xml:"min,attr"`
	Max     string `xml:"max,attr"`
	Step    string `xml:"step,attr"`
	Size    string `xml:"size,attr"`
	Value   string `xml:",chardata"`
}

// vectorType returns the property type for the tag of a def or set
// element, such as defNumberVector or setNumberVector.
func vectorType(tag string) (Type, bool) {
	for _, t := range []Type{NumberType, SwitchType, TextType, LightType, BLOBType} {
		if tag == "def"+string(t)+"Vector" || tag == "set"+string(t)+"Vector" {
			return t, true
		}
	}
	return "", false
}

func (e element) definition(t Type) (Property, error) {
	if e.Device == "" || e.Name == "" {
		return Property{}, fmt.Errorf("the %s element has no device or no name", e.XMLName.Local)
	}
	p := Property{
		Device: e.Device, Name: e.Name, Label: e.Label, Group: e.Group, Type: t,
		Perm: ReadOnly, State: Idle,
	}
	if t != LightType {
		perm := Permission(e.Perm)
		if perm != ReadOnly && perm != WriteOnly && perm != ReadWrite {
			return Property{}, fmt.Errorf("%s.%s has the permission %q, want ro, wo, or rw", e.Device, e.Name, e.Perm)
		}
		p.Perm = perm
	}
	if t == SwitchType {
		rule := Rule(e.Rule)
		if rule != OneOfMany && rule != AtMostOne && rule != AnyOfMany {
			return Property{}, fmt.Errorf("%s.%s has the switch rule %q, want OneOfMany, AtMostOne, or AnyOfMany", e.Device, e.Name, e.Rule)
		}
		p.Rule = rule
	}
	if err := e.applyAttributes(&p); err != nil {
		return Property{}, err
	}
	for _, m := range e.Members {
		member := Member{Name: m.Name, Label: m.Label}
		if err := m.apply(t, &member); err != nil {
			return Property{}, fmt.Errorf("reading the member %s.%s.%s: %w", e.Device, e.Name, m.Name, err)
		}
		p.Members = append(p.Members, member)
	}
	return p, nil
}

// updated applies a set*Vector to a copy of the property and returns
// the copy. When keepBLOBs is true, it also returns the decoded BLOBs
// of the update. Otherwise it records each BLOB's format and size and
// discards the data.
func (e element) updated(p Property, keepBLOBs bool) (Property, []BLOB, error) {
	p = p.clone()
	if err := e.applyAttributes(&p); err != nil {
		return Property{}, nil, err
	}
	var blobs []BLOB
	for _, m := range e.Members {
		i := memberIndex(p, m.Name)
		if i < 0 {
			return Property{}, nil, fmt.Errorf("the update for %s.%s names the member %q, which the property does not define", e.Device, e.Name, m.Name)
		}
		if err := m.apply(p.Type, &p.Members[i]); err != nil {
			return Property{}, nil, fmt.Errorf("reading the member %s.%s.%s: %w", e.Device, e.Name, m.Name, err)
		}
		if p.Type == BLOBType && keepBLOBs {
			blob, err := m.blob()
			if err != nil {
				return Property{}, nil, fmt.Errorf("reading the member %s.%s.%s: %w", e.Device, e.Name, m.Name, err)
			}
			blobs = append(blobs, blob)
		}
	}
	return p, blobs, nil
}

func memberIndex(p Property, name string) int {
	for i, m := range p.Members {
		if m.Name == name {
			return i
		}
	}
	return -1
}

// applyAttributes sets the attributes that def* and set* elements
// share. A set* element can leave any of them out.
func (e element) applyAttributes(p *Property) error {
	if e.State != "" {
		state, err := parseState(e.State)
		if err != nil {
			return fmt.Errorf("reading the state of %s.%s: %w", e.Device, e.Name, err)
		}
		p.State = state
	}
	// A bad timeout or timestamp does not fail the vector, because the
	// client does not act on either. The field keeps its old value.
	if seconds, err := strconv.ParseFloat(strings.TrimSpace(e.Timeout), 64); err == nil {
		p.Timeout = time.Duration(seconds * float64(time.Second))
	}
	if timestamp, ok := parseTimestamp(e.Timestamp); ok {
		p.Timestamp = timestamp
	}
	return nil
}

func parseState(text string) (State, error) {
	state := State(strings.TrimSpace(text))
	switch state {
	case Idle, Ok, Busy, Alert:
		return state, nil
	}
	return "", fmt.Errorf("the state %q is not Idle, Ok, Busy, or Alert", text)
}

// parseTimestamp reads INDI's timestamp, an ISO 8601 time in UTC with
// no zone designator, such as 2026-10-05T19:53:26. A fraction of a
// second is accepted.
func parseTimestamp(text string) (time.Time, bool) {
	timestamp, err := time.Parse("2006-01-02T15:04:05", strings.TrimSpace(text))
	return timestamp, err == nil
}

// apply sets the member's value and each other attribute that the
// element has. An attribute that the element leaves out keeps its value.
func (m member) apply(t Type, to *Member) error {
	switch t {
	case NumberType:
		for _, limit := range []struct {
			text string
			to   *float64
		}{{m.Value, &to.Number}, {m.Min, &to.Min}, {m.Max, &to.Max}, {m.Step, &to.Step}} {
			if limit.text == "" {
				continue
			}
			n, err := ParseNumber(limit.text)
			if err != nil {
				return err
			}
			*limit.to = n
		}
		if m.Format != "" {
			to.Format = m.Format
		}
	case SwitchType:
		switch strings.TrimSpace(m.Value) {
		case "On":
			to.Switch = true
		case "Off":
			to.Switch = false
		default:
			return fmt.Errorf("the switch value %q is not On or Off", m.Value)
		}
	case TextType:
		to.Text = strings.TrimSpace(m.Value)
	case LightType:
		state, err := parseState(m.Value)
		if err != nil {
			return err
		}
		to.Light = state
	case BLOBType:
		if m.Format != "" {
			to.BLOBFormat = m.Format
		}
		if size, err := strconv.Atoi(strings.TrimSpace(m.Size)); err == nil {
			to.BLOBSize = size
		}
	}
	return nil
}

// BLOB is one decoded BLOB from a server. Format is the file extension
// that the driver sets, such as ".fits", and Size is the length of Data
// in bytes.
type BLOB struct {
	Member string
	Format string
	Size   int
	Data   []byte
}

// blob decodes the base64 content of a oneBLOB. libindi wraps that
// content across lines, so the whitespace is removed first.
func (m member) blob() (BLOB, error) {
	encoded := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, m.Value)
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return BLOB{}, err
	}
	size, err := strconv.Atoi(strings.TrimSpace(m.Size))
	if err != nil {
		size = len(data)
	}
	return BLOB{Member: m.Name, Format: m.Format, Size: size, Data: data}, nil
}

// newVector writes a new*Vector, the element that a client sends to
// change a property, with every member of the property.
func newVector(p Property) []byte {
	return vector("new", p, "")
}

// setVector writes a set*Vector with the property's state. A driver
// sends this element to report a property. A client sends one only in
// Client.Relay, for a device that its own server does not hold.
func setVector(p Property) []byte {
	return vector("set", p, ` state=`+attribute(string(p.State)))
}

// vector writes a new*Vector or a set*Vector with every member of the
// property. attributes is text placed after the name attribute, either
// empty or starting with a space.
func vector(verb string, p Property, attributes string) []byte {
	var b bytes.Buffer
	tag := verb + string(p.Type) + "Vector"
	fmt.Fprintf(&b, "<%s device=%s name=%s%s>\n", tag, attribute(p.Device), attribute(p.Name), attributes)
	for _, m := range p.Members {
		var value string
		switch p.Type {
		case NumberType:
			value = strconv.FormatFloat(m.Number, 'g', -1, 64)
		case SwitchType:
			value = "Off"
			if m.Switch {
				value = "On"
			}
		case TextType:
			value = m.Text
		}
		one := "one" + string(p.Type)
		fmt.Fprintf(&b, "  <%s name=%s>", one, attribute(m.Name))
		b.WriteString(escape.Replace(value))
		fmt.Fprintf(&b, "</%s>\n", one)
	}
	fmt.Fprintf(&b, "</%s>\n", tag)
	return b.Bytes()
}

func getProperties() []byte {
	return []byte(`<getProperties version="` + protocolVersion + `"/>` + "\n")
}

func getDeviceProperties(device string) []byte {
	return []byte(`<getProperties version="` + protocolVersion + `" device=` + attribute(device) + `/>` + "\n")
}

func enableBLOB(r blobRequest) []byte {
	var b bytes.Buffer
	b.WriteString("<enableBLOB device=" + attribute(r.device))
	if r.property != "" {
		b.WriteString(" name=" + attribute(r.property))
	}
	b.WriteString(">" + string(r.mode) + "</enableBLOB>\n")
	return b.Bytes()
}

func pingReply(uid string) []byte {
	return []byte("<pingReply uid=" + attribute(uid) + "/>\n")
}

// escape writes the five characters that XML reserves as named
// entities, because libindi's parser (libs/indicore/lilxml.cpp) decodes
// named entities and no numeric references. encoding/xml would write an
// apostrophe as &#39; and a tab as &#x9;, and a driver would store those
// characters as they are, so its reply would not match the value sent.
// For the same reason, tabs and newlines are sent unescaped.
var escape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "'", "&apos;", `"`, "&quot;")

// attribute quotes and escapes an attribute value.
func attribute(value string) string {
	return `"` + escape.Replace(value) + `"`
}
