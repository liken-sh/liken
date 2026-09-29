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
		"goCache": func(mod, sum, family string, build bool) goCache {
			return goCache{Mod: mod, Keys: []string{sum}, Family: family, Build: build}
		},
	}).
	ParseFS(templateFiles, "templates/*.tmpl"))

// goCache is the data of the go-restore and go-save templates: the
// go.mod that names the Go version, the files whose hash keys the
// cache, the family whose entries a restore falls back to, and whether
// the cache holds the build cache too.
type goCache struct {
	Mod, Family string
	Keys        []string
	Build       bool
}

// HashFiles is the GitHub expression that hashes the key files.
func (g goCache) HashFiles() string {
	quoted := make([]string, len(g.Keys))
	for i, file := range g.Keys {
		quoted[i] = "'" + file + "'"
	}
	return "hashFiles(" + strings.Join(quoted, ", ") + ")"
}

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
	// Needs are the pinned components that the component depends on:
	// its images build on their targets, from the layers their jobs
	// write to the cache, so its checks start after theirs.
	Needs []string
	// PublishNeeds are the other components of its closure, whose
	// checks must pass before it publishes.
	PublishNeeds               []string
	Channel, Images, Publishes bool
}

type coverageDownload struct{ Component, Job, Dir string }

type site struct{ Component, Prefix string }

func rootData(components map[string]*Component) map[string]any {
	var list []rootComponent
	var coverage []coverageDownload
	var sites []site
	for _, c := range dependencyOrder(components) {
		var needs []string
		for _, dep := range slices.Sorted(slices.Values(c.Depends.Components)) {
			if components[dep].Pinned() {
				needs = append(needs, dep)
			}
		}
		list = append(list, rootComponent{
			Name:         c.Name(),
			Dir:          c.Dir,
			Needs:        needs,
			PublishNeeds: slices.DeleteFunc(Closure(components, c.Name()), func(n string) bool { return n == c.Name() }),
			Channel:      c.Outputs.Channel,
			Images:       len(c.Outputs.Images) > 0,
			Publishes:    c.HasOutputs(),
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
	// Cache is the job's Go cache, when Go is true.
	Cache goCache
}

func componentData(root string, c *Component) (map[string]any, error) {
	// The hooks in a component's .pre-commit-config.yaml run in CI only
	// through a prek job: the root workflow's repository job skips
	// every component's directory.
	if _, err := os.Stat(filepath.Join(root, c.Dir, ".pre-commit-config.yaml")); err == nil &&
		!slices.ContainsFunc(c.Jobs, func(j Job) bool { return j.Toolchain == "prek" }) {
		return nil, fmt.Errorf("%s has hooks in .pre-commit-config.yaml and no prek job to run them in CI", c.Name())
	}
	var jobs []jobData
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
		d.Cache = jobCache(c, d)
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
	}
	images := c.Outputs.Images
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
		"PublishTimeout": publishTimeout,
	}, nil
}

// jobCache is the Go cache of one job. Every manual reads the same
// modules, so the hugo jobs share one entry of modules, keyed on go.sum.
//
// Any other job keeps a build cache of its own. A saved entry never
// changes, and a restore that hits its key saves nothing, so an entry
// keyed on go.sum alone keeps what the job compiled when go.sum last
// changed. A job whose flags changed after that, such as a test run
// that adds -race, then compiles every dependency again on each run.
// So the key follows the files that choose what the job compiles and
// with which flags: the Makefile where the job runs, the component's
// package.toml, which holds the job's command, and its hooks, which a
// prek job runs.
func jobCache(c *Component, d jobData) goCache {
	sum := path.Join(d.ModuleDir, "go.sum")
	if d.Toolchain == "hugo" {
		return goCache{Mod: path.Join(d.ModuleDir, "go.mod"), Keys: []string{sum}, Family: "manuals"}
	}
	return goCache{Mod: path.Join(d.ModuleDir, "go.mod"), Family: d.ModuleDir, Build: true, Keys: []string{
		sum,
		path.Join(d.WorkDir, "Makefile"),
		path.Join(c.Dir, "package.toml"),
		path.Join(c.Dir, ".pre-commit-config.yaml"),
	}}
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
