package capture

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// A Definition is one property that a capture saw defined. Phase is
// the phase whose reply defined it first, and XML is the last
// definition it saw, as the server sent it with no timestamp.
type Definition struct {
	Name  string
	Phase string
	XML   []byte
}

// Definitions is the union of every definition in the replies, in the
// order of their first appearance. A capture keeps the definition of a
// property that the driver deleted later, because some drivers define
// a property and delete it at once, and some define a property only
// after a value changes. When a property is defined twice, the last
// definition wins, as it does in a client. A definition with no name
// is left out, because no client can address it: libindi defines one
// for a stream manager on a device that is not a CCD, and for the park
// position of a telescope whose park type has no position.
func Definitions(replies []Reply) ([]Definition, error) {
	var defs []Definition
	index := map[string]int{}
	for _, reply := range replies {
		elements, err := topLevel(reply.Data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", reply.Phase, err)
		}
		for _, e := range elements {
			if !strings.HasPrefix(e.kind, "def") || !strings.HasSuffix(e.kind, "Vector") || e.name == "" {
				continue
			}
			raw := timestamp.ReplaceAll(e.raw, nil)
			if i, seen := index[e.name]; seen {
				defs[i].XML = raw
				continue
			}
			index[e.name] = len(defs)
			defs = append(defs, Definition{Name: e.name, Phase: reply.Phase, XML: raw})
		}
	}
	return defs, nil
}

// The timestamp changes on every capture and says nothing about the
// property, so a capture leaves it out, and a capture again after an
// INDI bump differs only where libindi changed.
var timestamp = regexp.MustCompile(` timestamp="[^"]*"`)

type element struct {
	kind, name string
	raw        []byte
}

// topLevel splits a reply into its top-level elements, each with its
// bytes as the server sent them.
func topLevel(data []byte) ([]element, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var elements []element
	var current element
	start, depth := int64(0), 0
	for {
		offset := decoder.InputOffset()
		token, err := decoder.RawToken()
		if err == io.EOF {
			return elements, nil
		}
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				start = offset
				current = element{kind: t.Name.Local, name: attribute(t, "name")}
			}
			depth++
		case xml.EndElement:
			depth--
			if depth == 0 {
				current.raw = data[start:decoder.InputOffset()]
				elements = append(elements, current)
			}
		}
	}
}

func attribute(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// A Header names where a capture came from: the driver, the device it
// served, the version of the indi-bin package it ran on, and the
// interface bits in its DRIVER_INFO.
type Header struct {
	Driver    string `xml:"driver,attr"`
	Device    string `xml:"device,attr"`
	INDI      string `xml:"indi,attr"`
	Interface string `xml:"interface,attr"`
}

// Write writes a capture: the header as the root element's attributes,
// and the definitions grouped by the phase that defined them first.
// Every definition named in exclude is left out.
func Write(w io.Writer, header Header, defs []Definition, exclude map[string]bool) error {
	var out bytes.Buffer
	fmt.Fprintf(&out, "<capture driver=%s device=%s indi=%s interface=%s>\n",
		quote(header.Driver), quote(header.Device), quote(header.INDI), quote(header.Interface))
	phase := ""
	for _, d := range defs {
		if exclude[d.Name] {
			continue
		}
		if d.Phase != phase {
			if phase != "" {
				out.WriteString("</phase>\n")
			}
			phase = d.Phase
			fmt.Fprintf(&out, "<phase name=%s>\n", quote(phase))
		}
		out.Write(bytes.TrimSpace(d.XML))
		out.WriteString("\n")
	}
	if phase != "" {
		out.WriteString("</phase>\n")
	}
	out.WriteString("</capture>\n")
	_, err := w.Write(out.Bytes())
	return err
}

func quote(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return `"` + b.String() + `"`
}

// Interface reads DRIVER_INTERFACE from the DRIVER_INFO definition.
func Interface(defs []Definition) string {
	for _, d := range defs {
		if d.Name != "DRIVER_INFO" {
			continue
		}
		var info struct {
			Texts []struct {
				Name  string `xml:"name,attr"`
				Value string `xml:",chardata"`
			} `xml:"defText"`
		}
		if err := xml.Unmarshal(d.XML, &info); err != nil {
			return ""
		}
		for _, t := range info.Texts {
			if t.Name == "DRIVER_INTERFACE" {
				return strings.TrimSpace(t.Value)
			}
		}
	}
	return ""
}

// A Capture is a capture file as Read parses it: the header, and the
// name and kind of each property in each phase.
type Capture struct {
	Header
	Phases []struct {
		Name       string `xml:"name,attr"`
		Properties []struct {
			XMLName xml.Name
			Name    string `xml:"name,attr"`
		} `xml:",any"`
	} `xml:"phase"`
}

// Read parses a capture file.
func Read(r io.Reader) (Capture, error) {
	var c Capture
	err := xml.NewDecoder(r).Decode(&c)
	return c, err
}

// Defines reports whether the capture holds a definition of the
// property.
func (c Capture) Defines(property string) bool {
	for _, p := range c.Phases {
		for _, d := range p.Properties {
			if d.Name == property {
				return true
			}
		}
	}
	return false
}
