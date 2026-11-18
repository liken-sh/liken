package schema

import (
	"bufio"
	"bytes"
	"fmt"
	"path"
	"strings"
)

// The standard properties page of indilib/docs has one heading for
// each interface, with a table of properties under it. sections maps
// each heading to the name of that interface's captures, or to "" when
// the interface has no capture. readDocs fails on any heading other
// than Introduction that is not in this map, so a new section of the
// page must be added here.
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

// A documented property is one property as the page's table lists it.
// Name can contain a *, as in DIGITAL_OUTPUT_*, for a property that a
// driver defines once for each output.
type documented struct {
	Name        string
	Type        string
	Interface   string
	Description string
	Members     []docMember
}

type docMember struct {
	Name        string
	Description string
}

// matches reports whether a property's name matches the documented
// name, where a * matches any text.
func (d documented) matches(name string) bool {
	ok, _ := path.Match(d.Name, name)
	return ok
}

// readDocs reads every table of the page. A row with a name starts a
// property, and a value in that row is its first member, with the
// row's description. A row with no name belongs to the property above
// it: with a value it adds a member, and with no value its text extends
// the description of the last member, or of the property when it has
// no members. A value cell can hold two members joined by " or ".
func readDocs(page []byte) ([]documented, error) {
	var out []documented
	section, inTable := "", false
	scanner := bufio.NewScanner(bytes.NewReader(page))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if heading, ok := strings.CutPrefix(line, "## "); ok {
			if _, known := sections[heading]; !known && heading != "Introduction" {
				return nil, fmt.Errorf("the standard properties page has the section %q, which matches no interface", heading)
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

// unescape removes the Markdown formatting from a cell. The page's
// formatter treated underscores as emphasis and wrote DIGITAL_INPUT_*
// as DIGITAL*INPUT*\*. So a * with no backslash is an underscore, and
// \* is a literal *.
func unescape(cell string) string {
	cell = strings.ReplaceAll(cell, `\*`, "\x00")
	cell = strings.ReplaceAll(cell, `\_`, "_")
	cell = strings.ReplaceAll(cell, "*", "_")
	cell = strings.ReplaceAll(cell, "`", "")
	return strings.ReplaceAll(cell, "\x00", "*")
}
