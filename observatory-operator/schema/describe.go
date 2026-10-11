package schema

import (
	"strings"

	"github.com/liken-sh/liken/observatory-operator/indi"

	"github.com/liken-sh/liken/observatory-operator/indi/standard"
)

// sentence ends a description with a period.
func sentence(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text
}

// statusText describes the field of a property in status: the entry's
// description, the property it reports, and who changes it.
func (p property) statusText() string {
	from := "The status holds what the driver reports in `" + p.Name + "`."
	if p.Family == familyNumbered {
		from = "The status holds one item for each `" + strings.TrimRight(p.Name, "0123456789") + "n` that the driver defines."
	}
	if p.Family == familyNamed {
		from = "The status holds one item for each parameter that `" + p.NamedBy + "` names."
	}
	by := map[string]string{
		roleSetting: " `spec." + p.Field + "` sets it.",
		roleReading: "",
		roleCommand: " Clients such as KStars and Ekos change it.",
		roleTree:    " The operator decides it from the resources around the device, so it is not in spec.",
	}[p.Role]
	return sentence(p.Text) + " " + from + by
}

// specText describes the field of a setting in spec: the entry's
// description, and when the operator writes it.
func (p property) specText() string {
	target := "it to `" + p.Name + "`"
	if p.Family != "" {
		target = "each item to the property that the item names"
	}
	text := sentence(p.Text) + " The operator writes " + target + " once, in the `Configure` step of each reservation. Optional."
	if !p.driverElements() || len(p.Property.Members) == 0 {
		return text
	}
	example := "`" + p.Property.Members[0].Name + "`"
	switch {
	case p.Type == indi.TextType:
		return text
	case p.Type == indi.NumberType:
		return text + " It gives the value of each element that it names, such as " + example + "."
	case p.Rule == indi.AnyOfMany:
		return text + " It names the elements to turn on, of those that the driver defines, such as " + example + "."
	}
	return text + " It names one of the elements that the driver defines, such as " + example + "."
}

// memberText describes the field of one member: the entry's text for
// the member, or for the one value of a property with one member.
func (p property) memberText(m standard.Member, me memberEntry, unit string) string {
	if me.Description == "" {
		text := "The value that the driver reports."
		if unit != "" {
			text += " " + units[unit].sentence
		}
		return text
	}
	return sentence(me.Description) + " The member is `" + m.Name + "`."
}
