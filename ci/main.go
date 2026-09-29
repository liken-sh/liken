// ci reads the package.toml of every component in the repository and
// runs the parts of CI that need the whole graph: it writes the
// workflows, decides what each run builds and publishes, checks each
// component's declared dependencies against its code, publishes a
// component's images and deploy artifact, and writes the release
// record.
//
//	go run ./ci generate [-check]   write the workflows and the bake file, or check them
//	go run ./ci plan                decide what this workflow run does
//	go run ./ci deps                check each [depends] against the code
//	go run ./ci publish ...         push one component's outputs
//	go run ./ci record -tag T       write the GitHub release for a tag
//	go run ./ci sites               list each manual's directory and prefix
//	go run ./ci reports             list each coverage report that no manual serves
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	world := World{
		Published: Published{Registry: ghcr(""), Channel: "https://releases.liken.sh", Client: httpClient},
		Registry:  ghcr,
		Run:       ExecRunner,
		Getenv:    os.Getenv,
		Stdout:    os.Stdout,
	}
	if err := world.run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ci:", err)
		os.Exit(1)
	}
}

// World is everything outside the repository that a command reads or
// changes: the registry, the channel, the commands it runs, and the
// environment the workflow sets. The tests give each command a world
// of fixtures.
type World struct {
	Published Published
	// Registry returns a client of ghcr.io that uses the token.
	Registry func(token string) Registry
	Run      Runner
	Getenv   func(string) string
	Stdout   io.Writer
}

func (w World) run(args []string) error {
	stdout := w.Stdout
	if len(args) == 0 {
		return fmt.Errorf("name a command: generate, plan, deps, publish, record, sites, or reports")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	root := flags.String("root", ".", "the repository root")
	check := flags.Bool("check", false, "generate: fail when a workflow on disk differs, and write nothing")
	component := flags.String("component", "", "publish: the component")
	version := flags.String("version", "", "publish: the version")
	mode := flags.String("mode", "", "publish: dev or release")
	tag := flags.String("tag", "", "record: the release tag")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	components, err := LoadComponents(*root)
	if err != nil {
		return err
	}
	switch args[0] {
	case "generate":
		return generate(*root, components, *check, stdout)
	case "plan":
		return w.plan(*root, components)
	case "deps":
		return deps(*root, components, stdout)
	case "publish":
		c, ok := components[*component]
		if !ok {
			return fmt.Errorf("%q is not a component", *component)
		}
		git := Git{Dir: *root}
		commit, err := git.Commit("HEAD")
		if err != nil {
			return err
		}
		p := Publisher{Root: *root, Registry: w.Published.Registry, Run: w.Run, Commit: commit, Components: components}
		return p.Publish(c, *version, *mode)
	case "record":
		return w.record(*root, components, *tag)
	case "reports":
		for _, c := range sortedComponents(components) {
			if c.Docs != nil {
				continue
			}
			for _, job := range c.Jobs {
				if len(job.Coverage) > 0 {
					fmt.Fprintf(stdout, "%s:%s:%s\n", c.Dir, c.Name(), job.Coverage[0])
					break
				}
			}
		}
		return nil
	case "sites":
		for _, c := range sortedComponents(components) {
			if c.Docs != nil && c.Docs.Prefix != "" {
				fmt.Fprintf(stdout, "%s:%s\n", c.Dir, c.Docs.Prefix)
			}
		}
		return nil
	}
	return fmt.Errorf("%q is not a command", args[0])
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

func ghcr(token string) Registry {
	return Registry{Base: "https://ghcr.io", Owner: "liken-sh", Client: httpClient, Token: token}
}

func generate(root string, components map[string]*Component, check bool, stdout io.Writer) error {
	files, err := Workflows(root, components)
	if err != nil {
		return err
	}
	if files[bakeFile], err = Bake(root, components); err != nil {
		return err
	}
	if !check {
		return WriteWorkflows(root, files)
	}
	differ, err := CheckWorkflows(root, files)
	if err != nil {
		return err
	}
	if len(differ) > 0 {
		return fmt.Errorf("these files differ from what the generator writes; run `make workflows`: %s", strings.Join(differ, ", "))
	}
	fmt.Fprintln(stdout, "every generated workflow and the bake file are current")
	return nil
}

func deps(root string, components map[string]*Component, stdout io.Writer) error {
	checker := DepsChecker{Root: root, Components: components, GoList: GoList, CargoMetadata: CargoMetadata}
	findings, err := checker.Check()
	if err != nil {
		return err
	}
	for _, f := range findings {
		fmt.Fprintln(stdout, f)
	}
	if len(findings) > 0 {
		return fmt.Errorf("%d undeclared uses", len(findings))
	}
	fmt.Fprintln(stdout, "every package.toml names what its component uses")
	return nil
}

func (w World) record(root string, components map[string]*Component, tag string) error {
	if err := CheckReleaseTag(tag); err != nil {
		return err
	}
	p := w.Published
	git := Git{Dir: root}
	previous, _, describeErr := git.Describe(tag + "^")
	pinned := func(c *Component) (bool, string, error) {
		_, published, err := p.Recipe(c)
		if err != nil || describeErr != nil {
			return published, "", err
		}
		then, err := git.PinnedTagAt(previous, c)
		return published, then, err
	}
	entries, err := Record(components, p.Versions, pinned, tag)
	if err != nil {
		return err
	}
	digest := ""
	releases, err := p.channelReleases()
	if err != nil {
		return err
	}
	for _, r := range releases {
		if r.Version == tag {
			digest = r.Digest
		}
	}
	var changes []string
	subjects, logErr := "", error(nil)
	if describeErr == nil {
		subjects, logErr = git.run("log", "--format=%s", previous+".."+tag)
	} else {
		subjects, logErr = git.run("log", "--format=%s", tag)
	}
	if logErr != nil {
		return logErr
	}
	if subjects != "" {
		changes = strings.Split(subjects, "\n")
	}
	notes := RecordNotes(tag, entries, digest, changes)
	fmt.Fprint(w.Stdout, notes)
	dir, err := os.MkdirTemp("", "record")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(file, []byte(notes), 0o644); err != nil {
		return err
	}
	if err := w.Run(root, "gh", "release", "view", tag); err == nil {
		return w.Run(root, "gh", "release", "edit", tag, "--notes-file", file)
	}
	return w.Run(root, "gh", "release", "create", tag, "--verify-tag", "--title", tag, "--notes-file", file)
}
