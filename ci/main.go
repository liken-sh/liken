// ci reads the package.toml of every component in the repository and
// runs the parts of CI that need the whole graph: it writes the
// workflows and the Dependabot configuration, decides what each run
// builds and publishes, checks each component's declared dependencies
// against its code, publishes a component's images and deploy
// artifact, and writes the release record.
//
//	go run ./ci generate [-check]   write the workflows, the bake file, and dependabot.yml, or check them
//	go run ./ci plan                decide what this workflow run does
//	go run ./ci bases [-out FILE]   check each pinned base's published image, and write docker-bake.override.hcl
//	go run ./ci deps                check each [depends] against the code
//	go run ./ci pins                check the pins, the downloads, and the actions against AGENTS.md
//	go run ./ci namespaces          check that a namespace override moves no deploy object
//	go run ./ci selectors           check that each DeviceClass guards every attribute it reads
//	go run ./ci publish ...         push one component's outputs
//	go run ./ci scan -image I -version V -sarif DIR
//	                                scan one published image with Trivy
//	go run ./ci record -tag T       write the GitHub release for a tag
//	go run ./ci sites               list each manual's directory and prefix
//	go run ./ci reports -site URL -dest DIR
//	                                gather each coverage report's profiles, and list the reports to render
//	go run ./ci profile -component C -site URL
//	                                take C's coverage profiles from the site when they describe HEAD
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func main() {
	world := World{
		Published:   Published{Registry: ghcr(""), Channel: "https://releases.liken.sh", Client: httpClient},
		Registry:    ghcr,
		Run:         ExecRunner,
		VerifyImage: cosignVerify,
		Getenv:      os.Getenv,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
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
	// VerifyImage checks that a signature of the identity covers the
	// image reference.
	VerifyImage func(root, reference, identity string) error
	Getenv      func(string) string
	Stdout      io.Writer
	Stderr      io.Writer
}

func (w World) run(args []string) error {
	stdout := w.Stdout
	if len(args) == 0 {
		return fmt.Errorf("name a command: generate, plan, bases, deps, pins, namespaces, selectors, publish, scan, record, sites, reports, or profile")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	root := flags.String("root", ".", "the repository root")
	check := flags.Bool("check", false, "generate: fail when a workflow on disk differs, and write nothing")
	component := flags.String("component", "", "publish, profile: the component")
	version := flags.String("version", "", "publish, scan: the version")
	mode := flags.String("mode", "", "publish: dev or release")
	sarif := flags.String("sarif", "", "publish: the directory for the Trivy scan of each pushed image, or empty for no scan; scan: the directory for the scan")
	image := flags.String("image", "", "scan: the image")
	tag := flags.String("tag", "", "record: the release tag")
	site := flags.String("site", "", "reports, profile: the address of the site that serves the profiles now")
	dest := flags.String("dest", "", "reports: the site's tree, where the profiles publish")
	out := flags.String("out", "", "bases: the override file to write, or empty for docker-bake.override.hcl at the root")
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
	case "bases":
		return w.bases(*root, components, *out)
	case "deps":
		return deps(*root, components, stdout)
	case "pins":
		return pins(*root, stdout)
	case "namespaces":
		return namespaces(*root, stdout)
	case "selectors":
		return selectors(*root, stdout)
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
		created, err := git.CommitTime(commit)
		if err != nil {
			return err
		}
		p := Publisher{Root: *root, Registry: w.Published.Registry, Run: w.Run, Commit: commit, Created: created, Components: components, SARIF: *sarif, DockerConfig: dockerConfig(w.Getenv)}
		return p.Publish(c, *version, *mode)
	case "scan":
		return w.scan(*root, components, *image, *version, *sarif)
	case "record":
		return w.record(*root, components, *tag)
	case "reports":
		return gatherReports(*root, *site, *dest, w.Published.Client, components, stdout, w.Stderr)
	case "profile":
		c, ok := components[*component]
		if !ok {
			return fmt.Errorf("%q is not a component", *component)
		}
		if err := reuseProfiles(*root, *site, "HEAD", w.Published.Client, components, c); err != nil {
			return fmt.Errorf("%s: the site's coverage profile does not describe HEAD: %w", c.Name(), err)
		}
		fmt.Fprintf(stdout, "%s: took the coverage profile that the site serves\n", c.Name())
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
	if files[dependabotFile], err = Dependabot(root); err != nil {
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
	fmt.Fprintln(stdout, "every generated workflow, the bake file, and the Dependabot configuration are current")
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

func pins(root string, stdout io.Writer) error {
	drift, err := checkPins(root)
	if err != nil {
		return err
	}
	fetches, err := checkFetches(root)
	if err != nil {
		return err
	}
	manifests, err := checkManifests(root)
	if err != nil {
		return err
	}
	actions, err := checkActions(root)
	if err != nil {
		return err
	}
	builders, err := checkBuildKit(root)
	if err != nil {
		return err
	}
	actions = append(actions, builders...)
	for _, f := range slices.Concat(drift, fetches, manifests, actions) {
		fmt.Fprintln(stdout, f)
	}
	if len(drift) > 0 || len(fetches) > 0 || len(manifests) > 0 || len(actions) > 0 {
		return fmt.Errorf("%d pins drift, %d downloads break a rule in AGENTS.md, %d manifests disagree with their pins, and %d actions or builders run code that no commit named",
			len(drift), len(fetches), len(manifests), len(actions))
	}
	fmt.Fprintln(stdout, "every shared pin names one version, every download follows the rules in AGENTS.md, every pin that ships has a manifest that agrees with it, and every action and builder is pinned by commit")
	return nil
}

func selectors(root string, stdout io.Writer) error {
	findings, err := checkSelectors(root)
	if err != nil {
		return err
	}
	for _, f := range findings {
		fmt.Fprintln(stdout, f)
	}
	if len(findings) > 0 {
		return fmt.Errorf("%d reads of a device attribute have no has() before them, so a device without the attribute fails every claim of the class", len(findings))
	}
	fmt.Fprintln(stdout, "every DeviceClass guards each attribute it reads")
	return nil
}

func namespaces(root string, stdout io.Writer) error {
	findings, err := checkNamespaces(root)
	if err != nil {
		return err
	}
	for _, f := range findings {
		fmt.Fprintln(stdout, f)
	}
	if len(findings) > 0 {
		return fmt.Errorf("%d deploy objects would move under a namespace override", len(findings))
	}
	fmt.Fprintln(stdout, "no deploy object moves under a namespace override")
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

// scan runs Trivy on one published image of a component, for the root
// workflow's trivy job. A scan that fails prints a warning and returns
// no error, as scanImage says; only a request that names no image of
// the repository, or no version or directory, is an error.
func (w World) scan(root string, components map[string]*Component, image, version, dir string) error {
	if version == "" || dir == "" {
		return fmt.Errorf("a scan needs a -version and a -sarif directory")
	}
	for _, c := range components {
		for _, i := range c.Outputs.Images {
			if i.Name == image {
				scanImage(w.Run, root, dir, image, version)
				return nil
			}
		}
	}
	return fmt.Errorf("%q is not an image of any component", image)
}
