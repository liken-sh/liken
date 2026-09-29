package main

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
)

// The templates use {% %} as delimiters, because the workflows are full
// of GitHub's own ${{ }} expressions.
//
//go:embed templates/*.tmpl
var templateFiles embed.FS

var templates = template.Must(template.New("").
	Delims("{%", "%}").
	Funcs(template.FuncMap{
		"join":  strings.Join,
		"lines": func(s string) []string { return strings.Split(strings.TrimSpace(s), "\n") },
	}).
	ParseFS(templateFiles, "templates/*.tmpl"))

// Workflows renders every generated workflow, keyed by its path from
// the repository root.
func Workflows(root string, components map[string]*Component) (map[string][]byte, error) {
	files := map[string][]byte{}
	top, err := render("ci.yaml.tmpl", rootData(components))
	if err != nil {
		return nil, err
	}
	files[".github/workflows/ci.yaml"] = top
	for _, c := range sortedComponents(components) {
		data, err := componentData(root, c)
		if err != nil {
			return nil, err
		}
		out, err := render("component.yaml.tmpl", data)
		if err != nil {
			return nil, err
		}
		files[".github/workflows/component-"+c.Name()+".yaml"] = out
	}
	return files, nil
}

func render(name string, data any) ([]byte, error) {
	var out bytes.Buffer
	if err := templates.ExecuteTemplate(&out, name, data); err != nil {
		return nil, fmt.Errorf("rendering %s: %w", name, err)
	}
	// A template's last block leaves blank lines behind it, and a file
	// ends with exactly one newline.
	return append(bytes.TrimRight(out.Bytes(), "\n"), '\n'), nil
}

type rootComponent struct {
	Name, Dir string
	Needs     []string
	Channel   bool
}

type coverageDownload struct{ Component, Job, Dir string }

type site struct{ Component, Prefix string }

func rootData(components map[string]*Component) map[string]any {
	var list []rootComponent
	var coverage []coverageDownload
	var sites []site
	for _, c := range dependencyOrder(components) {
		list = append(list, rootComponent{
			Name:    c.Name(),
			Dir:     c.Dir,
			Needs:   slices.Sorted(slices.Values(c.Depends.Components)),
			Channel: c.Outputs.Channel,
		})
		for _, job := range c.Jobs {
			if len(job.Coverage) > 0 {
				coverage = append(coverage, coverageDownload{c.Name(), job.Name, c.Dir})
			}
		}
		if c.Docs != nil && c.Docs.Prefix != "" {
			sites = append(sites, site{c.Name(), c.Docs.Prefix})
		}
	}
	return map[string]any{"Components": list, "Coverage": coverage, "Sites": sites}
}

// dependencyOrder lists the components so that each one comes after
// everything it depends on, and by name where the graph does not
// decide.
func dependencyOrder(components map[string]*Component) []*Component {
	var order []*Component
	done := map[string]bool{}
	var visit func(c *Component)
	visit = func(c *Component) {
		if done[c.Name()] {
			return
		}
		done[c.Name()] = true
		deps := slices.Sorted(slices.Values(c.Depends.Components))
		for _, dep := range deps {
			visit(components[dep])
		}
		order = append(order, c)
	}
	for _, c := range sortedComponents(components) {
		visit(c)
	}
	return order
}

type jobData struct {
	Job
	Component, WorkDir, ModuleDir string
	// Go is true when the job sets up Go: a go or hugo job always, and
	// a prek job when its module directory has a go.mod, because its
	// hooks then run Go.
	Go bool
}

type imageData struct {
	Image
	Load, Pinned bool
}

func componentData(root string, c *Component) (map[string]any, error) {
	var jobs []jobData
	var needs []string
	for _, job := range c.Jobs {
		d := jobData{Job: job, Component: c.Name(), WorkDir: path.Join(c.Dir, job.Dir),
			ModuleDir: path.Join(c.Dir, orDefault(job.Module, job.Dir))}
		if d.Timeout == 0 {
			d.Timeout = 15
		}
		switch job.Toolchain {
		case "go", "hugo":
			d.Go = true
		case "prek":
			_, err := os.Stat(filepath.Join(root, d.ModuleDir, "go.mod"))
			d.Go = err == nil
		}
		for _, file := range job.Coverage {
			if strings.Contains(file, "/") {
				return nil, fmt.Errorf("%s: job %s: a coverage file must be at the component's top, not %q", c.Name(), job.Name, file)
			}
		}
		for i, file := range d.Coverage {
			d.Coverage[i] = path.Join(c.Dir, file)
		}
		artifacts := make([]string, len(job.Artifacts))
		for i, file := range job.Artifacts {
			artifacts[i] = path.Join(c.Dir, file)
		}
		d.Artifacts = artifacts
		jobs = append(jobs, d)
		needs = append(needs, job.Name)
	}
	var images []imageData
	for _, image := range c.Outputs.Images {
		images = append(images, imageData{Image: image, Load: len(image.Platforms) <= 1, Pinned: c.Pinned()})
	}
	if len(images) > 0 {
		needs = append(needs, "images")
	}
	imageTimeout, publishTimeout := 45, 30
	if c.Outputs.Channel {
		publishTimeout = 40
	}
	return map[string]any{
		"Name":           c.Name(),
		"Dir":            c.Dir,
		"Jobs":           jobs,
		"Images":         images,
		"ImageTimeout":   imageTimeout,
		"Publishes":      c.HasOutputs(),
		"Channel":        c.Outputs.Channel,
		"Deploy":         c.Outputs.Deploy,
		"NeedsList":      strings.Join(needs, ", "),
		"PublishTimeout": publishTimeout,
	}, nil
}

// WriteWorkflows writes the generated workflows under root, and
// removes a generated workflow whose component no longer exists.
func WriteWorkflows(root string, files map[string][]byte) error {
	stale, err := generatedFiles(root)
	if err != nil {
		return err
	}
	for name, data := range files {
		delete(stale, name)
		if err := os.WriteFile(filepath.Join(root, name), data, 0o644); err != nil {
			return err
		}
	}
	for name := range stale {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	return nil
}

// CheckWorkflows lists each generated workflow on disk that differs
// from what the generator writes, and each one that should not exist.
func CheckWorkflows(root string, files map[string][]byte) ([]string, error) {
	stale, err := generatedFiles(root)
	if err != nil {
		return nil, err
	}
	var differ []string
	for name, data := range files {
		delete(stale, name)
		current, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || !bytes.Equal(current, data) {
			differ = append(differ, name)
		}
	}
	for name := range stale {
		differ = append(differ, name)
	}
	slices.Sort(differ)
	return differ, nil
}

// generatedMark is the first line of every generated workflow.
const generatedMark = "# This file is generated"

// generatedFiles lists the workflows on disk that the generator wrote,
// by the mark on their first line.
func generatedFiles(root string) (map[string]bool, error) {
	matches, err := filepath.Glob(filepath.Join(root, ".github/workflows/*.yaml"))
	if err != nil {
		return nil, err
	}
	files := map[string]bool{}
	for _, match := range matches {
		data, err := os.ReadFile(match)
		if err != nil {
			return nil, err
		}
		if bytes.HasPrefix(data, []byte(generatedMark)) {
			rel, _ := filepath.Rel(root, match)
			files[filepath.ToSlash(rel)] = true
		}
	}
	return files, nil
}
