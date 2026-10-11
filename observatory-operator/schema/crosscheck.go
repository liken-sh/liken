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
// (indilib/docs#7), and its text is not ours to publish. So the
// generator never reads it. The cross-check reads it at development
// time and lists each place where it disagrees with the captures, the
// conventions, or the units of the fields files, for a person to
// judge. ReportFile holds that list.
const ReportFile = "disagreements.txt"

// crossCheck answers each disagreement between the page and the
// standard sets of the kinds: a type, a member, a property that the
// page lists and no source defines, and a unit.
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

// compareTypes answers each property of an interface whose type or
// members differ from the page's row.
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

// lookup answers the page's row for a property of an interface. The
// interface's own section comes first, then the general section, then
// any section, because one name can appear in several: the receiver's
// streaming properties are in the CCD's section. A row of another
// interface whose members share no name with the property's describes
// another property of the same name, such as the receiver's
// FITS_HEADER, and is not used. The page says that most CCD_
// properties apply to the guide head with GUIDER_ in place of CCD_, so
// a GUIDER_ property with no row of its own reads the row of its CCD_
// twin, with each member renamed the same way when the property
// defines the new name. GUIDER_INFO keeps the CCD_ names of its
// members.
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

// describes reports whether a row can describe a property: it names
// no member, or it names at least one of the property's members.
func (d documented) describes(p standard.Property) bool {
	if len(d.Members) == 0 || len(p.Members) == 0 {
		return true
	}
	return slices.ContainsFunc(p.Members, func(m standard.Member) bool {
		_, named := d.member(m.Name)
		return named
	})
}

// members answers each member that the page names and the structure
// lacks, and each member that the structure defines and the page's
// list of members lacks. A row with a * stands for members that the
// driver numbers, so it is not compared.
func members(p standard.Property, d documented, source string) []string {
	if len(d.Members) == 0 || slices.ContainsFunc(d.Members, func(m docMember) bool { return strings.Contains(m.Name, "*") }) {
		return nil
	}
	var report []string
	for _, m := range d.Members {
		if _, held := p.Member(m.Name); !held {
			report = append(report, fmt.Sprintf("%s: the standard properties page names the member %s, and %s does not define it", p.Name, m.Name, source))
		}
	}
	for _, m := range p.Members {
		if _, held := d.member(m.Name); !held {
			report = append(report, fmt.Sprintf("%s: %s defines the member %s, and the standard properties page does not name it", p.Name, source, numbered.ReplaceAllString(m.Name, "${1}*")))
		}
	}
	return report
}

// A driver numbers some members, such as FILTER_SLOT_NAME_1 to
// FILTER_SLOT_NAME_12, and the report names them once, as
// FILTER_SLOT_NAME_*.
var numbered = regexp.MustCompile(`^(.*_)[0-9]+$`)

func (d documented) member(name string) (docMember, bool) {
	for _, m := range d.Members {
		if ok, _ := path.Match(m.Name, name); ok {
			return m, true
		}
	}
	return docMember{}, false
}

// undefined answers each property that the page lists in the section
// of an interface, and that neither the interface's captures nor the
// conventions define.
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

// unitWords are the words of the page that name a unit, by the unit
// of a fields file, in the order that they are tried: a longer phrase
// before a phrase that it holds.
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

// pageUnit answers the unit that a text of the page names, or "".
func pageUnit(text string) string {
	for _, u := range unitWords {
		if u.word.MatchString(text) {
			return u.unit
		}
	}
	return ""
}

// compareUnits answers each number whose unit in the fields file
// differs from the unit that the page names for it.
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

// CrossCheck answers the report of the disagreements between the
// sources of the standard properties, as ReportFile holds it: the
// variants of each interface and the conventions against the
// simulators, which Load finds, and the page of indilib/docs at the
// commit named against the standard sets and the fields files. root is
// the component's directory.
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
