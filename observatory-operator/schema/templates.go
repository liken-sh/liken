package schema

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// Each kind with procedures has a CRD template in this directory,
// <plural>-crd.yaml.tmpl, which spells out the fields that only that
// kind has. It uses these template functions for the parts that several
// kinds share and the parts that come from INDI:
//
//   - {{ include "device spec" . }} and {{ include "device status" . }}
//     insert the fields that every device kind has, from
//     device.yaml.tmpl.
//   - {{ include "procedure spec" . }} and
//     {{ include "procedure status" . }} insert the procedure schema,
//     from procedures.yaml.tmpl. That schema includes the kind's own
//     "action" template for the action fields that only the kind has,
//     and the "action" template includes "action fields" for the rest.
//   - {{ include "fits keywords" . }} inserts spec.fitsKeywords, from
//     fitskeywords.yaml.tmpl. Its rule rejects the keywords that
//     {{ refusedKeywords }} lists. {{ astrophotographyKeywords }} lists
//     the ones among them that astrophotography-operator writes, so the
//     rule's message can name that operator.
//   - {{ settings }} and {{ standard }} insert the typed fields of the
//     kind's <plural>-fields.yaml, for spec and for status.
//
// "indent n" lets each fragment be written at the left margin and
// included at any depth.
const (
	templateSuffix  = "-crd.yaml.tmpl"
	deviceTemplate  = "device.yaml.tmpl"
	procedureSchema = "procedures.yaml.tmpl"
	fitsTemplate    = "fitskeywords.yaml.tmpl"
)

// settings and standard are the typed fields as YAML at the left
// margin, and are empty for a kind that has none.
func renderCRD(dir, plural, settings, standard string) (string, error) {
	t := template.New(plural)
	t.Funcs(template.FuncMap{
		"include": func(name string, data any) (string, error) {
			var out bytes.Buffer
			err := t.ExecuteTemplate(&out, name, data)
			return out.String(), err
		},
		"indent":          indent,
		"settings":        func() string { return settings },
		"standard":        func() string { return standard },
		"refusedKeywords": func() string { return celList(observatory.RefusedFITSKeywords()) },
		"astrophotographyKeywords": func() string {
			return celList(observatory.AstrophotographyFITSKeywords)
		},
	})
	for _, name := range []string{deviceTemplate, procedureSchema, fitsTemplate, plural + templateSuffix} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		if _, err := t.Parse(string(raw)); err != nil {
			return "", err
		}
	}
	var out bytes.Buffer
	if err := t.Execute(&out, nil); err != nil {
		return "", err
	}
	return tidy(out.String()), nil
}

// indent leaves blank lines empty.
func indent(n int, fragment string) string {
	lines := strings.Split(strings.Trim(fragment, "\n"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			lines[i] = strings.Repeat(" ", n) + line
		}
	}
	return strings.Join(lines, "\n")
}

// celList returns names as the items of a CEL list, such as
// 'SIMPLE', 'BITPIX'.
func celList(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = "'" + name + "'"
	}
	return strings.Join(quoted, ", ")
}
