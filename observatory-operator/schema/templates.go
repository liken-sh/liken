package schema

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// The CRD of each kind with procedures is a template in this
// directory, <plural>-crd.yaml.tmpl, with the fields that only that
// kind has written out, and directives for the parts that it shares
// with other kinds or that come from INDI:
//
//   - {{ include "device spec" . }} and {{ include "device status" . }}
//     are the fields that every device kind shares, in device.yaml.tmpl.
//   - {{ include "procedure spec" . }} and
//     {{ include "procedure status" . }} are the procedure schema, in
//     procedures.yaml.tmpl. Its actions include the kind's own
//     template "action", which states the action's fields that only
//     the kind has, and includes "action fields" for the rest.
//   - {{ settings }} and {{ standard }} are the typed fields that the
//     kind's fields file gives, in spec and in status.
//
// "indent n" indents a fragment by n columns, so a fragment is written
// once at the left margin and included at any depth.
const (
	templateSuffix  = "-crd.yaml.tmpl"
	deviceTemplate  = "device.yaml.tmpl"
	procedureSchema = "procedures.yaml.tmpl"
)

// renderCRD renders the template of one kind. The typed fields arrive
// as YAML at the left margin, empty for a kind with none.
func renderCRD(dir, plural, settings, standard string) (string, error) {
	t := template.New(plural)
	t.Funcs(template.FuncMap{
		"include": func(name string, data any) (string, error) {
			var out bytes.Buffer
			err := t.ExecuteTemplate(&out, name, data)
			return out.String(), err
		},
		"indent":   indent,
		"settings": func() string { return settings },
		"standard": func() string { return standard },
	})
	for _, name := range []string{deviceTemplate, procedureSchema, plural + templateSuffix} {
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

// indent puts n spaces before each line of a fragment that holds text.
func indent(n int, fragment string) string {
	lines := strings.Split(strings.Trim(fragment, "\n"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			lines[i] = strings.Repeat(" ", n) + line
		}
	}
	return strings.Join(lines, "\n")
}
