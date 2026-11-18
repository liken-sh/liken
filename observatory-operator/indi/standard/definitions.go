// Package standard reads the standard properties of each INDI
// interface from two sources and merges them. The captures of bare
// drivers in this directory give the properties that libindi defines,
// with their type, permission, rule, and members. conventions.yaml
// lists the properties that several drivers define the same way and
// libindi does not. Where the two sources differ, the package reports
// the difference and keeps the capture's definition.
package standard

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/indi"
)

// A Property is one standard property, as a driver defines it.
type Property struct {
	Name  string
	Type  indi.Type
	Perm  indi.Permission
	Rule  indi.Rule
	Label string
	// Members are in the order of the definition.
	Members []Member
	// Convention is true for a property of the conventions table,
	// which libindi does not define.
	Convention bool
}

// A Member is one element of a property.
type Member struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Format string `json:"format,omitempty"`
}

// Member returns the member with the given name.
func (p Property) Member(name string) (Member, bool) {
	for _, m := range p.Members {
		if m.Name == name {
			return m, true
		}
	}
	return Member{}, false
}

// definitions reads every def*Vector element in an INDI stream, at any
// depth, so it reads a capture file and a server transcript the same
// way. When a property is defined more than once, the first definition
// keeps its position in the result and later ones add their new
// members. It reads no values, because a value comes from one driver at
// one moment, such as the time in TIME_UTC.
func definitions(r io.Reader) ([]Property, error) {
	decoder := xml.NewDecoder(r)
	var out []Property
	index := map[string]int{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || !strings.HasPrefix(start.Name.Local, "def") || !strings.HasSuffix(start.Name.Local, "Vector") {
			continue
		}
		var vector struct {
			Name    string `xml:"name,attr"`
			Label   string `xml:"label,attr"`
			Perm    string `xml:"perm,attr"`
			Rule    string `xml:"rule,attr"`
			Members []struct {
				Name   string `xml:"name,attr"`
				Label  string `xml:"label,attr"`
				Format string `xml:"format,attr"`
			} `xml:",any"`
		}
		if err := decoder.DecodeElement(&vector, &start); err != nil {
			return nil, err
		}
		p := Property{
			Name:  vector.Name,
			Type:  indi.Type(strings.TrimSuffix(strings.TrimPrefix(start.Name.Local, "def"), "Vector")),
			Perm:  indi.Permission(vector.Perm),
			Rule:  indi.Rule(vector.Rule),
			Label: vector.Label,
		}
		for _, m := range vector.Members {
			p.Members = append(p.Members, Member{Name: m.Name, Label: m.Label, Format: m.Format})
		}
		if i, seen := index[p.Name]; seen {
			for _, m := range p.Members {
				if _, held := out[i].Member(m.Name); !held {
					out[i].Members = append(out[i].Members, m)
				}
			}
			continue
		}
		index[p.Name] = len(out)
		out = append(out, p)
	}
}

func definitionsOf(data []byte) ([]Property, error) {
	return definitions(bytes.NewReader(data))
}
