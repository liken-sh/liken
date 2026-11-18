package inditest

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Member is one member of a property: a number, a switch, a text, or a
// light. Only a number member uses Format, Min, Max, and Step.
type Member struct {
	Name, Label, Format, Min, Max, Step, Value string
}

// Property is one property of a fake driver. Type is Number, Switch,
// Text, Light, or BLOB.
type Property struct {
	Type                                          string
	Device, Name, Label, Group, State, Perm, Rule string
	Members                                       []Member
}

// Clone returns a copy that shares no Members slice with p.
func (p *Property) Clone() *Property {
	out := *p
	out.Members = slices.Clone(p.Members)
	return &out
}

// Value returns a member's value, and false when p has no such member.
func (p *Property) Value(member string) (string, bool) {
	for _, m := range p.Members {
		if m.Name == member {
			return m.Value, true
		}
	}
	return "", false
}

// SetValue sets a member's value. It does nothing when p has no such
// member.
func (p *Property) SetValue(member, value string) {
	for i := range p.Members {
		if p.Members[i].Name == member {
			p.Members[i].Value = value
		}
	}
}

// Switch turns one member On and every other member Off, as under
// OneOfMany.
func (p *Property) Switch(on string) {
	for i := range p.Members {
		p.Members[i].Value = "Off"
		if p.Members[i].Name == on {
			p.Members[i].Value = "On"
		}
	}
}

// Updated returns a copy of p with the state and member values from
// the vector v. When v has no state, the copy keeps p's state.
func (p *Property) Updated(v Vector) *Property {
	out := p.Clone()
	if v.State != "" {
		out.State = v.State
	}
	for _, m := range v.Members {
		out.SetValue(m.Name, strings.TrimSpace(m.Value))
	}
	return out
}

// Escape escapes text for an XML attribute or element.
func Escape(text string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(text))
	return b.String()
}

// Def returns the def*Vector element that defines p, in the XML form
// that indiserver sends.
func (p *Property) Def() string {
	var b strings.Builder
	fmt.Fprintf(&b, `<def%sVector device="%s" name="%s" label="%s" group="%s" state="%s"`, p.Type, Escape(p.Device), Escape(p.Name), Escape(p.Label), Escape(p.Group), p.State)
	if p.Type != "Light" {
		fmt.Fprintf(&b, ` perm="%s"`, p.Perm)
	}
	if p.Type == "Switch" {
		fmt.Fprintf(&b, ` rule="%s"`, p.Rule)
	}
	b.WriteString(` timeout="60">`)
	for _, m := range p.Members {
		fmt.Fprintf(&b, `<def%s name="%s" label="%s"`, p.Type, Escape(m.Name), Escape(m.Label))
		if p.Type == "Number" {
			fmt.Fprintf(&b, ` format="%s" min="%s" max="%s" step="%s"`, Escape(m.Format), m.Min, m.Max, m.Step)
		}
		fmt.Fprintf(&b, ">%s</def%s>", Escape(m.Value), p.Type)
	}
	fmt.Fprintf(&b, "</def%sVector>\n", p.Type)
	return b.String()
}

// Set returns the set*Vector element that reports p's state and
// values.
func (p *Property) Set() string {
	var b strings.Builder
	fmt.Fprintf(&b, `<set%sVector device="%s" name="%s" state="%s">`, p.Type, Escape(p.Device), Escape(p.Name), p.State)
	for _, m := range p.Members {
		fmt.Fprintf(&b, `<one%s name="%s">%s</one%s>`, p.Type, Escape(m.Name), Escape(m.Value), p.Type)
	}
	fmt.Fprintf(&b, "</set%sVector>\n", p.Type)
	return b.String()
}

// Vector is one XML element of a transcript or of a client's input: a
// def*Vector, a set*Vector, a new*Vector, or a getProperties.
type Vector struct {
	XMLName xml.Name
	Device  string `xml:"device,attr"`
	Name    string `xml:"name,attr"`
	Label   string `xml:"label,attr"`
	Group   string `xml:"group,attr"`
	State   string `xml:"state,attr"`
	Perm    string `xml:"perm,attr"`
	Rule    string `xml:"rule,attr"`
	Members []struct {
		Name   string `xml:"name,attr"`
		Label  string `xml:"label,attr"`
		Format string `xml:"format,attr"`
		Min    string `xml:"min,attr"`
		Max    string `xml:"max,attr"`
		Step   string `xml:"step,attr"`
		Value  string `xml:",chardata"`
	} `xml:",any"`
}

// Changes returns a new*Vector's members as "<member>=<value>", in the
// order the client sent them.
func (v Vector) Changes() []string {
	var out []string
	for _, m := range v.Members {
		out = append(out, m.Name+"="+strings.TrimSpace(m.Value))
	}
	return out
}

// Definitions returns the properties that a transcript defines, in
// order. A later definition of a property replaces an earlier one, and
// each set*Vector after a definition updates the property. So each
// property has the state and values that the driver reported last. For
// example, the telescope simulator defines TELESCOPE_PARK with both
// switches Off, and then reports UNPARK On.
func Definitions(transcript []byte) ([]*Property, error) {
	decoder := xml.NewDecoder(bytes.NewReader(transcript))
	decoder.Strict = false
	var out []*Property
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		var v Vector
		if err := decoder.DecodeElement(&v, &start); err != nil {
			return nil, err
		}
		tag := v.XMLName.Local
		if strings.HasPrefix(tag, "set") && strings.HasSuffix(tag, "Vector") {
			if i := slices.IndexFunc(out, func(o *Property) bool { return o.Name == v.Name }); i >= 0 {
				out[i] = out[i].Updated(v)
			}
			continue
		}
		if !strings.HasPrefix(tag, "def") || !strings.HasSuffix(tag, "Vector") || v.Name == "" {
			continue
		}
		p := &Property{Type: strings.TrimSuffix(strings.TrimPrefix(tag, "def"), "Vector"),
			Device: v.Device, Name: v.Name, Label: v.Label, Group: v.Group, State: v.State, Perm: v.Perm, Rule: v.Rule}
		for _, m := range v.Members {
			p.Members = append(p.Members, Member{m.Name, m.Label, m.Format, m.Min, m.Max, m.Step, strings.TrimSpace(m.Value)})
		}
		if i := slices.IndexFunc(out, func(o *Property) bool { return o.Name == p.Name }); i >= 0 {
			out[i] = p
		} else {
			out = append(out, p)
		}
	}
}
