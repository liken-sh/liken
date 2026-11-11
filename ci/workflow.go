package main

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// The templates use {% %} as delimiters, because the workflows are full
// of GitHub's own ${{ }} expressions.
//
//go:embed templates/*.tmpl
var templateFiles embed.FS

// prekVersion is the prek that every prek job and the repository job
// run, so a hook behaves the same in each.
const prekVersion = "0.5.9"

// prekSHA256 is the digest of prek's x86_64-unknown-linux-gnu archive
// at prekVersion, the archive that prek-action downloads on an
// ubuntu-26.04 runner. prek-action checks a download against its own
// table of checksums, and it runs a version that the table does not
// name without a check. So every prek step passes this digest as the
// action's sha256 input, and the action fails on a mismatch.
// `ci/latest.sh --bump prek` writes it with prekVersion, from the
// digest that the release publishes, after it verifies the release's
// GitHub attestation.
const prekSHA256 = "2fee13923018dd517c19dba032ddcae8010cadfb81bc47e742ad5e8981a3f126"

// fluxVersionFile holds the flux release that the OS ships. The
// publish job pushes each deploy artifact with the same flux CLI, so
// the artifact's format is the one that the cluster's flux reads.
const fluxVersionFile = "liken/flux/VERSION"

var templates = template.Must(template.New("").
	Delims("{%", "%}").
	Funcs(template.FuncMap{
		"action":       pinnedAction,
		"prekVersion":  func() string { return prekVersion },
		"prekSHA256":   func() string { return prekSHA256 },
		"trivyVersion": func() string { return trivyVersion },
		"trivySHA256":  func() string { return trivySHA256 },
		"trivyUpload": func(image, present string, dry bool) trivyUpload {
			return trivyUpload{Image: image, Present: present, Dry: dry}
		},
		"grypeUpload": func(name string, dry bool) grypeUpload {
			return grypeUpload{Name: name, Dry: dry}
		},
		"join":  strings.Join,
		"hcl":   hclString,
		"lines": func(s string) []string { return strings.Split(strings.TrimSpace(s), "\n") },
		"goCache": func(mod, sum, family string, build bool) goCache {
			return goCache{Mod: mod, Keys: []string{sum}, Family: family, Build: build, SavesModules: !build}
		},
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
	if files[scanFile], err = render("scan.yaml.tmpl", scanData(components)); err != nil {
		return nil, err
	}
	for _, c := range sortedComponents(components) {
		data, err := componentData(root, c)
		if err != nil {
			return nil, err
		}
		if c.Outputs.Deploy != "" {
			flux, err := os.ReadFile(filepath.Join(root, fluxVersionFile))
			if err != nil {
				return nil, fmt.Errorf("%s publishes a deploy artifact with the flux CLI, whose version is in %s: %w", c.Name(), fluxVersionFile, err)
			}
			data["Flux"] = strings.TrimPrefix(strings.TrimSpace(string(flux)), "v")
			if err := checkFluxAction(data["Flux"].(string)); err != nil {
				return nil, err
			}
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
	// SecurityEvents is true when a job of the component's workflow
	// uploads to code scanning: a CodeQL job, or the publish job, which
	// uploads the Trivy scan of each image it pushes or the grype scan
	// of the release it publishes to the channel. GitHub checks the
	// permissions of every job in a called workflow against the call,
	// including a job that the call skips, so both calls grant it.
	SecurityEvents bool
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
			SecurityEvents: len(c.Outputs.Images) > 0 || c.Outputs.Channel ||
				slices.ContainsFunc(c.Jobs, func(j Job) bool { return j.Toolchain == "codeql" }),
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
	return map[string]any{"Components": list, "Coverage": coverage, "Sites": sites, "TrivyCache": trivyCache}
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
	Component, Dir, WorkDir, ModuleDir string
	// Go is true when the job sets up Go: a go or hugo job always, and
	// a prek job when its module directory has a go.mod and a hook
	// that the job runs calls go or gofmt.
	Go bool
	// Cache is the job's Go cache, when Go is true.
	Cache goCache
	// Category is the code scanning category of a CodeQL job.
	Category string
	// Select is the name that the plan's list of jobs holds for this
	// job. The shards of a job run when the job runs, so they select
	// on the job's name.
	Select string
	// Matrix holds the number of each shard, in the shards of a job.
	Matrix []int
	// Needs names the shards that a sharded job waits for, and whose
	// coverage files it downloads.
	Needs string
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
		d := jobData{Job: job, Component: c.Name(), Dir: c.Dir, WorkDir: path.Join(c.Dir, job.Dir),
			ModuleDir: path.Join(c.Dir, orDefault(job.Module, job.Dir))}
		if d.Timeout == 0 {
			d.Timeout = 15
		}
		switch job.Toolchain {
		case "go", "hugo":
			d.Go = true
		case "prek":
			runsGo, err := hooksRunGo(root, c, job.Skip)
			if err != nil {
				return nil, err
			}
			_, noModule := os.Stat(filepath.Join(root, d.ModuleDir, "go.mod"))
			d.Go = runsGo && noModule == nil
		case "codeql":
			d.Go = job.Language == languageGo
			d.Category = codeqlCategory(c, job)
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
		d.Select = job.Name
		if job.Shards > 1 {
			jobs = append(jobs, shardsOf(d))
			d.Needs = shardsName(job.Name)
		}
		jobs = append(jobs, d)
	}
	if err := checkBuildCachePrefixes(c, jobs); err != nil {
		return nil, err
	}
	images := c.Outputs.Images
	imageTimeout, publishTimeout := 45, 30
	if c.Outputs.Channel {
		publishTimeout = 40
	}
	for _, image := range images {
		imageTimeout = max(imageTimeout, image.Timeout)
		publishTimeout = max(publishTimeout, image.Timeout)
	}
	publishTimeout = max(publishTimeout, c.Outputs.Timeout)
	var buildkit string
	if len(images) > 0 {
		var err error
		if buildkit, err = readBuildKit(root); err != nil {
			return nil, fmt.Errorf("%s builds images: %w", c.Name(), err)
		}
	}
	return map[string]any{
		"Name":           c.Name(),
		"Dir":            c.Dir,
		"Jobs":           jobs,
		"Images":         images,
		"BuildKit":       buildkit,
		"ImageTimeout":   imageTimeout,
		"Publishes":      c.HasOutputs(),
		"Channel":        c.Outputs.Channel,
		"Deploy":         c.Outputs.Deploy,
		"PublishTimeout": publishTimeout,
	}, nil
}

// goCommand matches a command line that runs go or gofmt, as a word
// of its own, so that cargo does not match.
var goCommand = regexp.MustCompile(`(^|[\s'"(;&|])(go|gofmt)(\s|$)`)

// hooksRunGo is true when a hook of the component's
// .pre-commit-config.yaml that is not in skip runs go or gofmt. A prek
// job that sets up Go also restores a build cache of about 1 GB, and a
// job whose Go hooks all run in the go job needs neither. Only a local
// hook runs the Go of the runner: a hook from a remote repository
// installs its own language. A component with no hooks runs no Go.
func hooksRunGo(root string, c *Component, skip []string) (bool, error) {
	file := filepath.Join(root, c.Dir, ".pre-commit-config.yaml")
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var config struct {
		Repos []struct {
			Repo  string `yaml:"repo"`
			Hooks []struct {
				ID    string `yaml:"id"`
				Entry string `yaml:"entry"`
			} `yaml:"hooks"`
		} `yaml:"repos"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return false, fmt.Errorf("%s: %w", file, err)
	}
	for _, repo := range config.Repos {
		if repo.Repo != "local" {
			continue
		}
		for _, hook := range repo.Hooks {
			if !slices.Contains(skip, hook.ID) && goCommand.MatchString(hook.Entry) {
				return true, nil
			}
		}
	}
	return false, nil
}

// shardsName names the matrix job of a sharded job. The name starts
// with shard- and not with the job's name, because the job's build
// cache restores every key that begins with its name and a dash
// (checkBuildCachePrefixes).
func shardsName(job string) string { return "shard-" + job }

// checkBuildCachePrefixes fails when one job's Go build cache would
// restore another's entries. A restore that misses its key takes the
// newest entry whose key begins with go-build-...-<job>-, so the entries
// of a job named <job>-<more> match too, and they hold another build.
func checkBuildCachePrefixes(c *Component, jobs []jobData) error {
	for _, a := range jobs {
		for _, b := range jobs {
			if a.Go && b.Go && a.Cache.Build && b.Cache.Build && strings.HasPrefix(b.Name, a.Name+"-") {
				return fmt.Errorf("%s: the job %q restores the build cache of job %q, because its restore takes every key that begins with its name and a dash", c.Name(), a.Name, b.Name)
			}
		}
	}
	return nil
}

// shardsOf answers the matrix job that runs a sharded job's ShardRun
// once for each shard. Each shard keeps the job's toolchain, caches,
// and timeout, and sets SHARD and SHARDS beside the job's own env.
func shardsOf(d jobData) jobData {
	shards := d
	shards.Name = shardsName(d.Name)
	shards.Run = d.ShardRun
	shards.Matrix = make([]int, d.Shards)
	for i := range shards.Matrix {
		shards.Matrix[i] = i + 1
	}
	shards.Env = map[string]string{}
	maps.Copy(shards.Env, d.Env)
	shards.Env["SHARD"] = "${{ matrix.shard }}"
	shards.Env["SHARDS"] = strconv.Itoa(d.Shards)
	shards.Artifacts = nil
	return shards
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
