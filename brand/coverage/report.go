// The report is one HTML file that embeds its stylesheet and uses no
// JavaScript, so it renders with no other file beside it. The summary
// tables link to each annotated source file with plain anchors.

package main

import (
	_ "embed"
	"fmt"
	"html/template"
	"strings"

	"github.com/liken-sh/liken/brand"
)

//go:embed report.css
var reportCSS string

// The view types hold the values that the template prints, so the
// template computes nothing.
type reportView struct {
	Title      string
	Stylesheet template.CSS
	Sources    []sourceView
}

type sourceView struct {
	ID      string
	Label   string
	Unit    string
	Covered int
	Total   int
	Percent string
	Files   []fileView
}

type fileView struct {
	ID      string
	Name    string
	Covered int
	Total   int
	Percent string
	Open    bool
	Lines   []lineView
}

type lineView struct {
	Number int
	Class  string
	Text   string
}

// Render returns the HTML report for the given inputs. Each input has
// its own summary table, because the inputs count in different units
// and a combined table would suggest that the numbers add up.
func Render(title string, sources []*Source) ([]byte, error) {
	page := reportView{
		Title:      title,
		Stylesheet: template.CSS(brand.Stylesheet + reportCSS),
	}
	used := map[string]bool{}
	for i, source := range sources {
		view := sourceView{
			ID:      fmt.Sprintf("s%d", i),
			Label:   source.Label,
			Unit:    source.Unit,
			Covered: source.Covered,
			Total:   source.Total,
			Percent: percent(source.Covered, source.Total),
		}
		for _, file := range source.Files {
			view.Files = append(view.Files, viewOf(file, fmt.Sprintf("f%d", i), used))
		}
		page.Sources = append(page.Sources, view)
	}

	var b strings.Builder
	if err := reportTemplate.Execute(&b, page); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// viewOf builds the view of one file. A file with uncovered lines
// starts open and any other file starts closed, so the page shows the
// files that need work.
func viewOf(file File, prefix string, used map[string]bool) fileView {
	view := fileView{
		ID:      identifier(prefix+"-"+file.Name, used),
		Name:    file.Name,
		Covered: file.Covered,
		Total:   file.Total,
		Percent: percent(file.Covered, file.Total),
		Open:    file.Covered < file.Total,
	}
	if file.Text == "" {
		return view
	}
	// Without TrimSuffix, the final newline makes Split return an empty
	// last line that the file does not have.
	lines := strings.Split(strings.TrimSuffix(file.Text, "\n"), "\n")
	for number, text := range lines {
		class := "line"
		if mark := file.Marks[number+1]; mark != Unmarked {
			class += " " + mark.String()
		}
		view.Lines = append(view.Lines, lineView{Number: number + 1, Class: class, Text: text})
	}
	return view
}

// identifier converts a file name to an HTML id of lower case letters
// and digits, with each run of other characters replaced by one
// hyphen. Different names can produce the same id, so used records the
// ids already assigned, and a repeat gets a numeric suffix.
func identifier(name string, used map[string]bool) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case !strings.HasSuffix(b.String(), "-"):
			b.WriteRune('-')
		}
	}
	id := strings.Trim(b.String(), "-")
	for at := 2; used[id]; at++ {
		id = fmt.Sprintf("%s-%d", strings.Trim(b.String(), "-"), at)
	}
	used[id] = true
	return id
}

// The template puts no newline between the line spans, because each
// span is a block element. A newline between them would render as an
// extra empty line under every source line.
var reportTemplate = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{ .Title }}</title>
<style>
{{ .Stylesheet }}
</style>
</head>
<body>
<h1>{{ .Title }}</h1>
<table>
<thead>
<tr><th>Input</th><th>Unit</th><th>Covered</th><th>Total</th><th>Percent</th></tr>
</thead>
<tbody>
{{- range .Sources }}
<tr><td><a href="#{{ .ID }}">{{ .Label }}</a></td><td>{{ .Unit }}</td><td>{{ .Covered }}</td><td>{{ .Total }}</td><td>{{ .Percent }}</td></tr>
{{- end }}
</tbody>
</table>
{{ range .Sources }}
<h2 id="{{ .ID }}">{{ .Label }}</h2>
<p>{{ .Covered }} of {{ .Total }} {{ .Unit }}, {{ .Percent }}.</p>
<table>
<thead>
<tr><th>File</th><th>Covered {{ .Unit }}</th><th>Total {{ .Unit }}</th><th>Percent</th></tr>
</thead>
<tbody>
{{- range .Files }}
<tr><td><a href="#{{ .ID }}">{{ .Name }}</a></td><td>{{ .Covered }}</td><td>{{ .Total }}</td><td>{{ .Percent }}</td></tr>
{{- end }}
</tbody>
</table>
{{ range .Files }}
<details class="file" id="{{ .ID }}"{{ if .Open }} open{{ end }}>
<summary>{{ .Name }} <span class="percent">{{ .Percent }}</span></summary>
{{- if .Lines }}
<pre class="source"><code>
{{- range .Lines }}<span class="{{ .Class }}"><span class="number">{{ .Number }}</span>{{ .Text }}</span>{{ end -}}
</code></pre>
{{- else }}
<p class="missing">This file is not in the tree the report was built from.</p>
{{- end }}
</details>
{{ end }}
{{- end }}
</body>
</html>
`))
