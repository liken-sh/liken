package schema

import (
	"bufio"
	"bytes"
	"fmt"
	"path"
	"strings"
)

// The standard properties page of indilib/docs groups its tables by
// interface, under one heading each. sections names the interface of
// each heading, by the name of its captures. A heading of an interface
// that has no capture maps to "". A heading that this map lacks fails
// the read, so a person places each new section of the page.
var sections = map[string]string{
	"General Properties":                                    "",
	"Telescope Properties":                                  "telescope",
	"CCD Properties":                                        "ccd",
	"CCD Streaming":                                         "ccd",
	"Filter Wheel Properties":                               "filter",
	"Focuser Properties":                                    "focuser",
	"Dome Properties":                                       "dome",
	"Input Interface Properties":                            "input",
	"Output Interface Properties":                           "output",
	"Light Box Interface Properties":                        "lightbox",
	"Dust Cap Interface Properties":                         "dustcap",
	"GPS Interface Properties":                              "gps",
	"Weather Interface Properties":                          "weather",
	"Rotator Interface Properties":                          "rotator",
	"Power Interface Properties":                            "",
	"IMU Interface Properties":                              "",
	"Polar Alignment Correction (PAC) Interface Properties": "pac",
}

// A documented property is one property as a table of the page gives
// it. Name can hold a *, as in DIGITAL_OUTPUT_*, for a property that a
// driver defines once for each output.
type documented struct {
	Name        string
	Type        string
	Interface   string
	Description string
	Members     []docMember
}

// A docMember is one member that a row of the page names.
type docMember struct {
	Name        string
	Description string
}

// matches reports whether the page's name covers a property's name.
func (d documented) matches(name string) bool {
	ok, _ := path.Match(d.Name, name)
	return ok
}

// readDocs reads every table of the page. A row with a name starts a
// property, and a row with no name adds a member to it, or continues
// the description of the last member or of the property when its
// value cell is empty. A value in a property's own row is its first
// member, and the row's description is then the member's and the
// property's. A value cell can name two members joined by " or ".
func readDocs(page []byte) ([]documented, error) {
	var out []documented
	section, inTable := "", false
	scanner := bufio.NewScanner(bytes.NewReader(page))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if heading, ok := strings.CutPrefix(line, "## "); ok {
			if _, known := sections[heading]; !known && heading != "Introduction" {
				return nil, fmt.Errorf("the standard properties page has a section %q that no interface is named for", heading)
			}
			section, inTable = heading, false
			continue
		}
		if !strings.HasPrefix(line, "|") {
			inTable = false
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) != 4 || cells[0] == "Name" || strings.HasPrefix(cells[0], "---") {
			inTable = len(cells) == 4
			continue
		}
		if !inTable {
			continue
		}
		name, kind, values, text := unescape(cells[0]), cells[1], unescape(cells[2]), cells[3]
		if name != "" {
			out = append(out, documented{Name: name, Type: kind, Interface: sections[section], Description: text})
			if values != "" {
				addMembers(&out[len(out)-1], values, text)
			}
			continue
		}
		if len(out) == 0 {
			continue
		}
		last := &out[len(out)-1]
		switch {
		case values != "":
			addMembers(last, values, text)
		case len(last.Members) > 0:
			m := &last.Members[len(last.Members)-1]
			m.Description = strings.TrimSpace(m.Description + " " + text)
		default:
			last.Description = strings.TrimSpace(last.Description + " " + text)
		}
	}
	return out, scanner.Err()
}

func addMembers(d *documented, values, text string) {
	for _, name := range strings.Split(values, " or ") {
		d.Members = append(d.Members, docMember{Name: strings.TrimSpace(name), Description: text})
	}
}

// unescape undoes the page's Markdown in a name. The page's formatter
// wrote DIGITAL_INPUT_* as DIGITAL*INPUT*\*, reading the underscores
// as emphasis, so an unescaped * stands for an underscore and an
// escaped one for a *.
func unescape(cell string) string {
	cell = strings.ReplaceAll(cell, `\*`, "\x00")
	cell = strings.ReplaceAll(cell, `\_`, "_")
	cell = strings.ReplaceAll(cell, "*", "_")
	cell = strings.ReplaceAll(cell, "`", "")
	return strings.ReplaceAll(cell, "\x00", "*")
}
