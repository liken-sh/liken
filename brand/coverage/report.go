// The page: from the inputs to one HTML file.
//
// The file stands alone. A site publishes it beside the manual, and
// a release ships it, so it carries its own stylesheet, asks for
// nothing over the network, and needs no JavaScript. The summary
// links into the annotated sources with plain anchors, and a file
// with nothing missing opens closed with a details element.
package main

import (
	_ "embed"
	"fmt"
	"html/template"
	"strings"

	"github.com/liken-sh/brand"
)

//go:embed report.css
var reportCSS string

// The views below are what the template draws. They hold strings and
// counts alone, so the template chooses no numbers and finds no
// files.
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

// Render draws the whole report. Every input keeps its own summary
// table, because the inputs count in different units and one table
// of both would read as one number.
func Render(title string, sources []*Source) ([]byte, error) {
	page := reportView{
		Title:      title,
		Stylesheet: template.CSS(brand.Stylesheet + reportCSS),
	}
	taken := map[string]bool{}
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
			view.Files = append(view.Files, viewOf(file, fmt.Sprintf("f%d", i), taken))
		}
		page.Sources = append(page.Sources, view)
	}

	var b strings.Builder
	if err := reportTemplate.Execute(&b, page); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// viewOf prepares one file: its id, its numbers, and one view for
// every line of its text. A file with lines to look at opens open,
// and a file with none opens closed, so the page shows the work that
// is left and keeps the rest within reach.
func viewOf(file File, prefix string, taken map[string]bool) fileView {
	view := fileView{
		ID:      identifier(prefix+"-"+file.Name, taken),
		Name:    file.Name,
		Covered: file.Covered,
		Total:   file.Total,
		Percent: percent(file.Covered, file.Total),
		Open:    file.Covered < file.Total,
	}
	if file.Text == "" {
		return view
	}
	// A file's last line ends with a newline, and splitting on it
	// leaves an empty last field that is no line of the file.
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

// identifier turns a name into an anchor a link can hold: lower
// case, with every run of other characters as one hyphen. Two names
// can fold onto one identifier, so each one is taken once and the
// next gets a number, and no link on the page lands on the wrong
// file.
func identifier(name string, taken map[string]bool) string {
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
	for at := 2; taken[id]; at++ {
		id = fmt.Sprintf("%s-%d", strings.Trim(b.String(), "-"), at)
	}
	taken[id] = true
	return id
}

// The source lines carry no newline between them, because each line
// is a block of its own. A newline would draw a second, empty line
// under every line of every file.
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
