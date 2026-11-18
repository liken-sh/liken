package schema

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/indi/standard"
)

// The standard properties page of indilib/docs is maintained by hand
// (indilib/docs#7), and its text may not be published, so the generator
// never reads it. make crosscheck reads it and writes ReportFile, which
// lists each place where the page disagrees with the captures, the
// conventions, or the units of the fields files, for a person to
// review.
const ReportFile = "disagreements.txt"

// crossCheck returns a line for each type, member, or unit on the page
// that differs from the standard sets and the kinds' fields files, and
// for each property that the page lists and no source defines.
func crossCheck(page []byte, sets map[string]standard.Interface, kinds []kind) ([]string, error) {
	docs, err := readDocs(page)
	if err != nil {
		return nil, err
	}
	var report []string
	for _, name := range sortedKeys(sets) {
		report = append(report, compareTypes(sets[name], docs)...)
	}
	report = append(report, undefined(sets, docs)...)
	for _, k := range kinds {
		for _, iface := range k.Interfaces {
			for _, p := range k.Fields {
				if _, own := sets[iface].Property(p.Name); own {
					report = append(report, compareUnits(k, iface, p, docs)...)
				}
			}
		}
	}
	slices.Sort(report)
	return slices.Compact(report), nil
}

func source(iface string, p standard.Property) string {
	if p.Convention {
		return "conventions.yaml"
	}
	return "the capture of " + iface
}

// compareTypes returns a line for each property of an interface whose
// type or members differ from the page's row.
func compareTypes(i standard.Interface, docs []documented) []string {
	var report []string
	for _, p := range i.Properties {
		d, found := lookup(docs, i.Name, p)
		if !found {
			continue
		}
		if d.Type != string(p.Type) {
			report = append(report, fmt.Sprintf("%s: the standard properties page gives the type %q, and %s gives %q", p.Name, d.Type, source(i.Name, p), p.Type))
			continue
		}
		if d.Interface == i.Name {
			report = append(report, members(p, d, source(i.Name, p))...)
		}
	}
	return report
}

// lookup returns the page's row for a property of an interface. One
// name can appear in several sections, so lookup tries the interface's
// own section, then the general section, then any section, because the
// page lists some properties under another interface, such as the
// receiver's streaming properties under the CCD. Outside the
// interface's own section, a row must pass describes, because a row
// whose members share no name with the property's members is another
// property of the same name, such as the receiver's FITS_HEADER.
//
// The page says that most CCD_ properties apply to the guide head with
// GUIDER_ in place of CCD_. So a GUIDER_ property with no row of its
// own uses the row of the CCD_ property with the same suffix. A member
// of that row is renamed to GUIDER_ only when the property defines the
// GUIDER_ name, because GUIDER_INFO keeps the CCD_ names of its members.
func lookup(docs []documented, iface string, p standard.Property) (documented, bool) {
	for _, section := range []func(documented) bool{
		func(d documented) bool { return d.Interface == iface },
		func(d documented) bool { return d.Interface == "" },
		func(documented) bool { return true },
	} {
		for _, d := range docs {
			if section(d) && d.matches(p.Name) && (d.Interface == iface || d.describes(p)) {
				return d, true
			}
		}
	}
	twin, ok := strings.CutPrefix(p.Name, "GUIDER_")
	if !ok {
		return documented{}, false
	}
	d, found := lookup(docs, iface, standard.Property{Name: "CCD_" + twin})
	d.Members = slices.Clone(d.Members)
	for k := range d.Members {
		renamed := strings.Replace(d.Members[k].Name, "CCD_", "GUIDER_", 1)
		if _, defined := p.Member(renamed); defined {
			d.Members[k].Name = renamed
		}
	}
	return d, found
}

// describes reports whether a row can describe a property: true when
// either lists no members, or when the row lists one of the property's
// members.
func (d documented) describes(p standard.Property) bool {
	if len(d.Members) == 0 || len(p.Members) == 0 {
		return true
	}
	return slices.ContainsFunc(p.Members, func(m standard.Member) bool {
		_, named := d.member(m.Name)
		return named
	})
}

// members returns a line for each member that only one of the page and
// the property has. A member name with a * on the page stands for
// members that the driver numbers, and members compares no row that
// has one.
func members(p standard.Property, d documented, source string) []string {
	if len(d.Members) == 0 || slices.ContainsFunc(d.Members, func(m docMember) bool { return strings.Contains(m.Name, "*") }) {
		return nil
	}
	var report []string
	for _, m := range d.Members {
		if _, defined := p.Member(m.Name); !defined {
			report = append(report, fmt.Sprintf("%s: the standard properties page names the member %s, and %s does not define it", p.Name, m.Name, source))
		}
	}
	for _, m := range p.Members {
		if _, listed := d.member(m.Name); !listed {
			report = append(report, fmt.Sprintf("%s: %s defines the member %s, and the standard properties page does not name it", p.Name, source, numbered.ReplaceAllString(m.Name, "${1}*")))
		}
	}
	return report
}

// numbered matches the names of members that a driver numbers, such as
// FILTER_SLOT_NAME_1, so the report writes them as FILTER_SLOT_NAME_*.
var numbered = regexp.MustCompile(`^(.*_)[0-9]+$`)

func (d documented) member(name string) (docMember, bool) {
	for _, m := range d.Members {
		if ok, _ := path.Match(m.Name, name); ok {
			return m, true
		}
	}
	return docMember{}, false
}

// undefined returns a line for each property that the page lists in an
// interface's section and that neither the capture of that interface
// nor the conventions define.
func undefined(sets map[string]standard.Interface, docs []documented) []string {
	var report []string
	for _, d := range docs {
		i, captured := sets[d.Interface]
		if !captured {
			continue
		}
		if !slices.ContainsFunc(i.Properties, func(p standard.Property) bool { return d.matches(p.Name) }) {
			report = append(report, fmt.Sprintf("%s: the standard properties page lists it for %s, and no source defines it", d.Name, d.Interface))
		}
	}
	return report
}

// unitWords maps the words that the page uses for a unit to the unit
// as a fields file writes it. pageUnit returns the first match, so a
// phrase comes before any shorter phrase inside it.
var unitWords = []struct {
	unit string
	word *regexp.Regexp
}{
	{"degC/min", regexp.MustCompile(`(?i)degrees celsius per minute`)},
	{"degC", regexp.MustCompile(`(?i)\bcelsius\b`)},
	{"arcsec/s", regexp.MustCompile(`(?i)\barcseconds per second\b`)},
	{"ms", regexp.MustCompile(`(?i)\bmilliseconds?\b`)},
	{"s", regexp.MustCompile(`(?i)\bseconds?\b`)},
	{"h", regexp.MustCompile(`(?i)\bhours\b`)},
	{"deg", regexp.MustCompile(`(?i)\bdegrees?\b`)},
	{"um", regexp.MustCompile(`(?i)\bmicrons?\b`)},
	{"mm", regexp.MustCompile(`\bmm\b`)},
	{"rpm", regexp.MustCompile(`(?i)\brpm\b`)},
	{"MB", regexp.MustCompile(`\bMB\b`)},
	{"m", regexp.MustCompile(`\(m\)|\bmeters\b`)},
	{"percent", regexp.MustCompile(`%`)},
	{"steps", regexp.MustCompile(`(?i)\bsteps\b`)},
}

func pageUnit(text string) string {
	for _, u := range unitWords {
		if u.word.MatchString(text) {
			return u.unit
		}
	}
	return ""
}

// compareUnits returns a line for each number whose unit in the fields
// file differs from the unit that the page gives for it.
func compareUnits(k kind, iface string, p property, docs []documented) []string {
	if p.Type != "Number" {
		return nil
	}
	d, found := lookup(docs, iface, p.Property)
	if !found {
		return nil
	}
	var report []string
	differ := func(where, ours, text string) {
		if theirs := pageUnit(text); theirs != "" && ours != "" && theirs != ours {
			report = append(report, fmt.Sprintf("%s: the standard properties page gives the unit %s, and %s gives %s", where, theirs, k.Plural+fieldsSuffix, ours))
		}
	}
	if len(p.Property.Members) == 1 || p.driverElements() {
		text := d.Description
		if m, ok := d.member(p.Property.Members[0].Name); ok && len(d.Members) == 1 {
			text += " " + m.Description
		}
		differ(p.Name, p.Unit, text)
		return report
	}
	for _, m := range p.Property.Members {
		if dm, ok := d.member(m.Name); ok {
			differ(p.Name+"."+m.Name, p.MemberUnits[m.Name].Unit, dm.Description)
		}
	}
	return report
}

// CrossCheck returns the contents of ReportFile. It joins the
// disagreements that Load finds between the variants of each interface
// and between the conventions and the simulators, with those that
// crossCheck finds on page, the standard properties page of
// indilib/docs at commit. root is the component's directory.
func CrossCheck(root string, page []byte, commit string) ([]byte, error) {
	sets, report, kinds, err := loadKinds(root)
	if err != nil {
		return nil, err
	}
	paged, err := crossCheck(page, sets, kinds)
	if err != nil {
		return nil, err
	}
	report = append(report, paged...)
	slices.Sort(report)
	var b strings.Builder
	fmt.Fprintf(&b, `# Written by make crosscheck; DO NOT EDIT.
#
# Each line is one place where the sources of the standard properties
# disagree: the captures of the bare drivers, conventions.yaml, the
# simulator transcripts, the units of the fields files in schema/, and
# the standard properties page of indilib/docs. The generator reads
# none of the page, so no line here changes the schema. A person
# decides what each line means.
#
# indilib/docs commit: %s
`, commit)
	for _, line := range slices.Compact(report) {
		b.WriteString(line + "\n")
	}
	return []byte(b.String()), nil
}
