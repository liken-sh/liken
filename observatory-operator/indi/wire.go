// The INDI wire protocol, version 1.7: the elements that a server sends
// and the conversion of each into the store's types, and the elements
// that a client sends. The protocol is a stream of XML elements with no
// root element, so the reader decodes one top-level element at a time.
// The reference is https://docs.indilib.org/protocol/ and the client
// side of libindi, libs/indiabstractclient/ in indilib/indi.

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

// protocolVersion is INDIV in libs/indicore/indiapi.h. INDI 2.x still
// sends 1.7: its changes are between the server and its drivers.
const protocolVersion = "1.7"

// element is any top-level element that a server sends. The fields
// cover the attributes of every element the client reads, and an
// element leaves the others empty.
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
	// wholeDevice marks a delProperty with no name attribute.
	wholeDevice bool
}

// member is one def* or one* child of a vector. A one* element in an
// update carries only the attributes that changed, so an empty
// attribute means no change.
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

// vectorType reads the type from a def or set element's name, such as
// defNumberVector or setNumberVector.
func vectorType(tag string) (Type, bool) {
	for _, t := range []Type{NumberType, SwitchType, TextType, LightType, BLOBType} {
		if tag == "def"+string(t)+"Vector" || tag == "set"+string(t)+"Vector" {
			return t, true
		}
	}
	return "", false
}

// definition converts a def*Vector into a property.
func (e element) definition(t Type) (Property, error) {
	if e.Device == "" || e.Name == "" {
		return Property{}, fmt.Errorf("%s with no device or no name", e.XMLName.Local)
	}
	p := Property{
		Device: e.Device, Name: e.Name, Label: e.Label, Group: e.Group, Type: t,
		Perm: ReadOnly, State: Idle,
	}
	if t != LightType {
		perm := Permission(e.Perm)
		if perm != ReadOnly && perm != WriteOnly && perm != ReadWrite {
			return Property{}, fmt.Errorf("%s.%s: permission %q", e.Device, e.Name, e.Perm)
		}
		p.Perm = perm
	}
	if t == SwitchType {
		rule := Rule(e.Rule)
		if rule != OneOfMany && rule != AtMostOne && rule != AnyOfMany {
			return Property{}, fmt.Errorf("%s.%s: rule %q", e.Device, e.Name, e.Rule)
		}
		p.Rule = rule
	}
	if err := e.applyAttributes(&p); err != nil {
		return Property{}, err
	}
	for _, m := range e.Members {
		member := Member{Name: m.Name, Label: m.Label}
		if err := m.apply(t, &member); err != nil {
			return Property{}, fmt.Errorf("%s.%s.%s: %w", e.Device, e.Name, m.Name, err)
		}
		p.Members = append(p.Members, member)
	}
	return p, nil
}

// updated applies a set*Vector to a copy of the property it names.
// When keepBLOBs is set, it also returns the BLOBs that the update
// carries, decoded. Otherwise it records each BLOB's format and size
// and drops its data.
func (e element) updated(p Property, keepBLOBs bool) (Property, []BLOB, error) {
	p = p.clone()
	if err := e.applyAttributes(&p); err != nil {
		return Property{}, nil, err
	}
	var blobs []BLOB
	for _, m := range e.Members {
		i := memberIndex(p, m.Name)
		if i < 0 {
			return Property{}, nil, fmt.Errorf("%s.%s has no member %q", e.Device, e.Name, m.Name)
		}
		if err := m.apply(p.Type, &p.Members[i]); err != nil {
			return Property{}, nil, fmt.Errorf("%s.%s.%s: %w", e.Device, e.Name, m.Name, err)
		}
		if p.Type == BLOBType && keepBLOBs {
			blob, err := m.blob()
			if err != nil {
				return Property{}, nil, fmt.Errorf("%s.%s.%s: %w", e.Device, e.Name, m.Name, err)
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

// applyAttributes applies the attributes that a definition and an
// update share. An update can leave each one out.
func (e element) applyAttributes(p *Property) error {
	if e.State != "" {
		state, err := parseState(e.State)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", e.Device, e.Name, err)
		}
		p.State = state
	}
	// A timeout or a timestamp that does not parse loses no value the
	// client acts on, so the client keeps the vector and leaves the
	// field as it was.
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
	return "", fmt.Errorf("state %q", text)
}

// parseTimestamp reads INDI's timestamp, an ISO 8601 time in UTC with
// no zone, such as 2026-10-05T19:53:26, with or without a fraction of
// a second.
func parseTimestamp(text string) (time.Time, bool) {
	timestamp, err := time.Parse("2006-01-02T15:04:05", strings.TrimSpace(text))
	return timestamp, err == nil
}

// apply sets one member's value and the attributes the element
// carries.
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
			return fmt.Errorf("switch %q", m.Value)
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

// BLOB is one BLOB that a server sent, decoded. Format is the file
// extension the driver gives, such as ".fits", and Size is the size of
// Data in bytes.
type BLOB struct {
	Member string
	Format string
	Size   int
	Data   []byte
}

// blob decodes a oneBLOB's base64 content, which libindi breaks into
// lines.
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

// newVector writes a new*Vector, the element a client sends to change a
// property. It writes every member of the property.
func newVector(p Property) []byte {
	var b bytes.Buffer
	tag := "new" + string(p.Type) + "Vector"
	fmt.Fprintf(&b, "<%s device=%s name=%s>\n", tag, attribute(p.Device), attribute(p.Name))
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
		xml.EscapeText(&b, []byte(value))
		fmt.Fprintf(&b, "</%s>\n", one)
	}
	fmt.Fprintf(&b, "</%s>\n", tag)
	return b.Bytes()
}

func getProperties() []byte {
	return []byte(`<getProperties version="` + protocolVersion + `"/>` + "\n")
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

// attribute quotes and escapes an attribute value.
func attribute(value string) string {
	var b bytes.Buffer
	b.WriteByte('"')
	xml.EscapeText(&b, []byte(value))
	b.WriteByte('"')
	return b.String()
}
