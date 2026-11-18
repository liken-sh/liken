package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// recorder is a Runner that records each command and runs nothing.
// For a bake with --metadata-file, it writes the file that buildx
// would write, with the digest that fakeDigest returns for the target,
// which is the last argument of the bake. Each publish uses a new
// temporary path for that file, so the recorded command shows it as
// <metadata>.
//
// When registry is set, a bake with --push adds the image to it under
// each tag. The image has the labels from the --set arguments and the
// labels in inherited, as when an outside base passes its labels into
// the image. A flux push of an artifact adds its tag.
type recorder struct {
	commands  []string
	registry  *fakeRegistry
	inherited map[string]string
}

func (r *recorder) run(dir, name string, args ...string) error {
	if name == "flux" && len(args) > 2 && args[0] == "push" && r.registry != nil {
		pkg, tag, _ := strings.Cut(strings.TrimPrefix(args[2], "oci://ghcr.io/liken-sh/"), ":")
		r.registry.mu.Lock()
		if r.registry.tags == nil {
			r.registry.tags = map[string][]string{}
		}
		r.registry.tags[pkg] = append(r.registry.tags[pkg], tag)
		r.registry.mu.Unlock()
	}
	i := slices.Index(args, "--metadata-file")
	if i < 0 {
		r.commands = append(r.commands, name+" "+strings.Join(args, " "))
		return nil
	}
	path := args[i+1]
	args = slices.Clone(args)
	args[i+1] = "<metadata>"
	r.commands = append(r.commands, name+" "+strings.Join(args, " "))
	target := args[len(args)-1]
	data, err := json.Marshal(map[string]map[string]string{target: {"containerimage.digest": fakeDigest(target)}})
	if err != nil {
		return err
	}
	if slices.Contains(args, "--push") && r.registry != nil {
		labels := maps.Clone(r.inherited)
		if labels == nil {
			labels = map[string]string{}
		}
		var references []string
		for j, arg := range args {
			if arg != "--set" || j+1 == len(args) {
				continue
			}
			if label, ok := strings.CutPrefix(args[j+1], target+".labels."); ok {
				key, value, _ := strings.Cut(label, "=")
				labels[key] = value
			}
			if reference, ok := strings.CutPrefix(args[j+1], target+".tags=ghcr.io/liken-sh/"); ok {
				references = append(references, reference)
			}
		}
		for _, reference := range references {
			pkg, tag, _ := strings.Cut(reference, ":")
			r.registry.push(pkg, tag, fakeDigest(target), labels)
		}
	}
	return os.WriteFile(path, data, 0o644)
}

// fixtureCommit and fixtureCreated are the commit hash and committer
// time that the publish fixtures use.
const fixtureCommit = "0123456789abcdef0123456789abcdef01234567"

var fixtureCreated = time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC)

// addPublishedTags adds each tag to the fixture's registry as an
// earlier publish at the same commit would have left it. An image tag
// gets the labels that the publish writes, and a deploy artifact gets
// only its tag.
func addPublishedTags(t *testing.T, p Publisher, fake *fakeRegistry, tags map[string][]string) {
	t.Helper()
	images := map[string]bool{}
	for _, c := range sortedComponents(p.Components) {
		for _, image := range c.Outputs.Images {
			images[image.Name] = true
			for _, version := range tags[image.Name] {
				recipe := ""
				if c.Pinned() {
					var err error
					if recipe, err = Recipe(p.Root, p.Components, c); err != nil {
						t.Fatalf("computing the recipe of the component: %v", err)
					}
				}
				m, err := p.metadata(c, image, version, recipe)
				if err != nil {
					t.Fatalf("reading the metadata of the image: %v", err)
				}
				fake.push(image.Name, version, fakeDigest(image.Name), m.Labels)
			}
		}
	}
	for pkg, list := range tags {
		if !images[pkg] {
			fake.tags[pkg] = append(fake.tags[pkg], list...)
		}
	}
}

// fixturePublisher returns a Publisher for the tree at root. Its
// recorder pushes into a fake registry that already has the given
// tags.
func fixturePublisher(t *testing.T, root string, tags map[string][]string) (Publisher, *recorder, map[string]*Component) {
	t.Helper()
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	fake := &fakeRegistry{tags: map[string][]string{}}
	rec := &recorder{registry: fake}
	p := Publisher{Root: root, Registry: fake.serve(t), Run: rec.run, Commit: fixtureCommit, Created: fixtureCreated, Components: components, DockerConfig: fixtureDockerConfig}
	addPublishedTags(t, p, fake, tags)
	return p, rec, components
}

// fixtureDockerConfig is the Docker configuration directory of the
// publish fixtures. In CI, docker/login-action writes the registry
// credential there, and cosign uses it to push signatures.
const fixtureDockerConfig = "/home/runner/.docker"

// fakeDigest returns the digest of the image that the recorder's bake
// pushes for a target.
func fakeDigest(target string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(target)))
}

// fixtureCosign is the cosign image in the images.toml of the publish
// fixtures.
const fixtureCosign = "ghcr.io/sigstore/cosign/cosign:v3.1.4@sha256:0f06154836d2de34f887a5c273506d52ca43edaa1d9e53e6510fa78a48f69c2d"

// fixtureScanner is the SBOM generator image in the images.toml of the
// publish fixtures.
const fixtureScanner = "docker/buildkit-syft-scanner:1.12.1@sha256:873a7458c3c8ac8e32c7b796c10e1600b07b963d2aa000a72a2d97aaaa2d6315"

const fixtureImages = "[build]\ncosign = \"" + fixtureCosign + "\"\nbuildkit-syft-scanner = \"" + fixtureScanner + "\"\n"

func publishFixture(t *testing.T, tags map[string][]string) (Publisher, *recorder, *Component) {
	t.Helper()
	root := writeTree(t, map[string]string{
		"operator/package.toml": `[package]
name = "operator"
license = "MIT"
[docs]
prefix = "operator"
[outputs]
deploy = "deploy"
[[outputs.images]]
name = "operator"
description = "The operator image."
target = "operator"
aliases = ["operator-sidecar"]
contexts = { brand = "brand" }
[[outputs.images]]
name = "operator-cli"
description = "The operator-cli image."
file = "Dockerfile.cli"
platforms = ["linux/amd64", "linux/arm64"]
`,
		"operator/deploy/kustomization.yaml": `# The base.
images:
  - name: ghcr.io/liken-sh/operator
    newTag: latest
`,
		"operator/Dockerfile":     "FROM scratch AS operator\n",
		"operator/Dockerfile.cli": "FROM scratch\n",
		"toolchains/images.toml":  fixtureImages,
	})
	p, rec, components := fixturePublisher(t, root, tags)
	return p, rec, components["operator"]
}

// metadataArgs returns the --set arguments that write each key=value
// as a label and as an annotation of the index and the manifest. The
// publish writes the keys in sorted order, so pass them sorted.
func metadataArgs(target string, keyValues ...string) string {
	var labels, annotations string
	for _, kv := range keyValues {
		labels += " --set " + target + ".labels." + kv
		annotations += " --set " + target + ".annotations=index,manifest:" + kv
	}
	return labels + annotations
}

// The publish writes every OCI key onto each image, as a label in its
// config and as an annotation on its index and its manifest.
func TestAReleasePushesEveryImageThenTheDeployArtifactThenLatest(t *testing.T) {
	p, rec, c := publishFixture(t, map[string][]string{"operator": {"2026.09.27-001"}})
	if err := p.Publish(c, "2026.10.02-001", publishRelease); err != nil {
		t.Fatalf("publishing the component: %v", err)
	}
	labels := func(target string) string {
		return metadataArgs(target,
			"org.opencontainers.image.created=2026-10-02T09:30:00Z",
			"org.opencontainers.image.description=The "+target+" image.",
			"org.opencontainers.image.documentation=https://liken.sh/operator/",
			"org.opencontainers.image.licenses=MIT",
			"org.opencontainers.image.revision=0123456789abcdef0123456789abcdef01234567",
			"org.opencontainers.image.source=https://github.com/liken-sh/liken",
			"org.opencontainers.image.title="+target,
			"org.opencontainers.image.url=https://liken.sh/",
			"org.opencontainers.image.vendor=liken",
			"org.opencontainers.image.version=2026.10.02-001",
		)
	}
	attest := func(target string) string {
		return " --set " + target + ".attest=type=sbom,generator=" + fixtureScanner + " --set " + target + ".attest=type=provenance,mode=max --metadata-file <metadata> " + target
	}
	want := []string{
		"docker buildx bake --file docker-bake.hcl --push --set operator.args.VERSION=2026.10.02-001" + labels("operator") +
			" --set operator.tags=ghcr.io/liken-sh/operator:2026.10.02-001 --set operator.tags=ghcr.io/liken-sh/operator-sidecar:2026.10.02-001" + attest("operator"),
		signCommand("ghcr.io/liken-sh/operator@" + fakeDigest("operator")),
		signCommand("ghcr.io/liken-sh/operator-sidecar@" + fakeDigest("operator")),
		"docker buildx imagetools create --tag ghcr.io/liken-sh/operator:latest ghcr.io/liken-sh/operator:2026.10.02-001",
		"docker buildx imagetools create --tag ghcr.io/liken-sh/operator-sidecar:latest ghcr.io/liken-sh/operator:2026.10.02-001",
		"docker buildx bake --file docker-bake.hcl --push --set operator-cli.args.VERSION=2026.10.02-001" + labels("operator-cli") +
			" --set operator-cli.tags=ghcr.io/liken-sh/operator-cli:2026.10.02-001" + attest("operator-cli"),
		signCommand("ghcr.io/liken-sh/operator-cli@" + fakeDigest("operator-cli")),
		"docker buildx imagetools create --tag ghcr.io/liken-sh/operator-cli:latest ghcr.io/liken-sh/operator-cli:2026.10.02-001",
	}
	if !reflect.DeepEqual(rec.commands[:len(want)], want) {
		t.Fatalf("the commands are:\n%s\nwant them to start with:\n%s", strings.Join(rec.commands, "\n"), strings.Join(want, "\n"))
	}
	rest := rec.commands[len(want):]
	if len(rest) != 2 || !strings.HasPrefix(rest[0], "flux push artifact oci://ghcr.io/liken-sh/operator-deploy:2026.10.02-001 --path ") ||
		rest[1] != "flux tag artifact oci://ghcr.io/liken-sh/operator-deploy:2026.10.02-001 --tag latest" {
		t.Errorf("the commands after the images are:\n%s\nwant the push of the deploy artifact, then its move to latest", strings.Join(rest, "\n"))
	}
}

// The publish reads the layer cache that the images job writes, and
// that job sets no build argument except VERSION. Another build
// argument can change a step's cache key even when no Dockerfile
// declares it. BuildKit uses SOURCE_DATE_EPOCH as the created time of
// each WORKDIR step, so a publish that set it would miss the cache from
// the first step of every stage and build each image again.
func TestThePublishSetsNoBuildArgumentExceptVersion(t *testing.T) {
	p, rec, c := publishFixture(t, nil)
	if err := p.Publish(c, "2026.10.02-001", publishRelease); err != nil {
		t.Fatalf("publishing the component: %v", err)
	}
	if got := buildArguments(rec.commands); !reflect.DeepEqual(got, []string{"VERSION"}) {
		t.Errorf("the publish sets the build arguments %v, and the images job sets only VERSION", got)
	}
}

// buildArguments returns the sorted names of the build arguments that
// the commands set on a bake target, without duplicates.
func buildArguments(commands []string) []string {
	var names []string
	for _, match := range regexp.MustCompile(`\.args\.([A-Za-z0-9_]+)=`).FindAllStringSubmatch(strings.Join(commands, "\n"), -1) {
		names = append(names, match[1])
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// The deploy artifact records its commit and uses the commit's time as
// its created time, in place of the push time that flux writes by
// default. It also has the same OCI keys as an image.
func TestTheDeployArtifactRecordsTheCommitTime(t *testing.T) {
	p, rec, c := publishFixture(t, nil)
	if err := p.Publish(c, "2026.10.02-001", publishRelease); err != nil {
		t.Fatalf("publishing the component: %v", err)
	}
	var push string
	for _, command := range rec.commands {
		if strings.HasPrefix(command, "flux push artifact ") {
			push = command
		}
	}
	want := " --source https://github.com/liken-sh/liken --revision 2026.10.02-001@sha1:0123456789abcdef0123456789abcdef01234567" +
		" --annotations org.opencontainers.image.created=2026-10-02T09:30:00Z" +
		" --annotations org.opencontainers.image.description=The Kubernetes manifests of operator, which name its images at the same version." +
		" --annotations org.opencontainers.image.documentation=https://liken.sh/operator/" +
		" --annotations org.opencontainers.image.licenses=MIT" +
		" --annotations org.opencontainers.image.title=operator-deploy" +
		" --annotations org.opencontainers.image.url=https://liken.sh/" +
		" --annotations org.opencontainers.image.vendor=liken" +
		" --annotations org.opencontainers.image.version=2026.10.02-001"
	if !strings.HasSuffix(push, want) {
		t.Errorf("the push of the deploy artifact:\n%s\nwant it to end with:\n%s", push, want)
	}
}

// The publish reads the labels of each pushed image back before it
// signs the image. A label that the publish did not write, such as one
// that an outside base starts to carry, fails the publish, and the
// image is not signed.
func TestAPushWhoseLabelsDifferIsNeverSigned(t *testing.T) {
	p, rec, c := publishFixture(t, nil)
	rec.inherited = map[string]string{"maintainer": "a person"}
	err := p.Publish(c, "2026.10.02-001", publishRelease)
	if err == nil || !strings.Contains(err.Error(), `maintainer="a person"`) {
		t.Fatalf("the publish returned %v, want the error for the label that the publish did not write", err)
	}
	if signs := signCommands(rec.commands); len(signs) > 0 {
		t.Errorf("the publish signed:\n%s\nwant no signature", strings.Join(signs, "\n"))
	}
}

// The fake registry responds immediately, so the pause between reads of
// a pushed digest is set to zero.
func init() {
	pushedLabelPause = 0
}

// A registry can return 404 for a digest that it accepted a moment
// before. The publish reads the labels again, so a late response does
// not fail a publish whose image is in the registry.
func TestAPublishWaitsForTheRegistryToServeWhatItPushed(t *testing.T) {
	p, rec, c := publishFixture(t, nil)
	rec.registry.lag = 2

	if err := p.Publish(c, "2026.10.02-001", publishRelease); err != nil {
		t.Fatalf("publishing the component: %v", err)
	}

	if signs := signCommands(rec.commands); len(signs) == 0 {
		t.Error("the publish signed nothing")
	}
}

// When the registry never serves the pushed digest, the publish fails
// and the image is not signed.
func TestAPushThatTheRegistryNeverServesIsNeverSigned(t *testing.T) {
	p, rec, c := publishFixture(t, nil)
	rec.registry.lag = 1000

	err := p.Publish(c, "2026.10.02-001", publishRelease)

	if !errors.Is(err, errNoImage) {
		t.Fatalf("the publish returned %v, want the error that the registry serves no image", err)
	}
	if signs := signCommands(rec.commands); len(signs) > 0 {
		t.Errorf("the publish signed:\n%s\nwant no signature", strings.Join(signs, "\n"))
	}
}

// When a publish runs again after a failure, it checks the labels of
// the image that the failed run pushed before it signs the image, as a
// first run does.
func TestARunAgainChecksThePublishedLabelsBeforeItSigns(t *testing.T) {
	p, rec, c := publishFixture(t, nil)
	rec.registry.push("operator", "2026.10.02-001", fakeDigest("operator"), map[string]string{"org.opencontainers.image.version": "2026.10.02-001"})
	err := p.Publish(c, "2026.10.02-001", publishRelease)
	if err == nil || !strings.Contains(err.Error(), "ghcr.io/liken-sh/operator:2026.10.02-001 differ from the labels the publish wrote") {
		t.Fatalf("the publish returned %v, want the error for labels that differ", err)
	}
	if signs := signCommands(rec.commands); len(signs) > 0 {
		t.Errorf("the publish signed:\n%s\nwant no signature", strings.Join(signs, "\n"))
	}
}

// trivyCommands returns the Trivy scans among the recorded commands.
func trivyCommands(commands []string) []string {
	var scans []string
	for _, command := range commands {
		if strings.HasPrefix(command, "trivy ") {
			scans = append(scans, command)
		}
	}
	return scans
}

// The publish scans each image right after it pushes it, and scans only
// an image that it pushes. A dry run pushes nothing, and a published
// version is not pushed again.
func TestThePublishScansEachImageThatItPushes(t *testing.T) {
	scan := func(dir, image, version string) string {
		return "trivy image --scanners vuln --ignore-unfixed --format sarif --output " + filepath.Join(dir, image+".sarif") + " ghcr.io/liken-sh/" + image + ":" + version
	}
	cases := []struct {
		name, version, mode string
		tags                []string
		scans               []string
	}{
		{"a release", "2026.10.02-001", publishRelease, nil,
			[]string{"operator", "operator-cli"}},
		{"a development build", "2026.10.02-001-dev-017-abcdef01", publishDev, nil,
			[]string{"operator", "operator-cli"}},
		{"a dry run", "2026.10.02-001", publishDry, nil, nil},
		{"a version that is already published", "2026.10.02-001", publishRelease, []string{"2026.10.02-001"},
			[]string{"operator-cli"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, rec, component := publishFixture(t, map[string][]string{"operator": c.tags})
			p.SARIF = t.TempDir()
			if err := p.Publish(component, c.version, c.mode); err != nil {
				t.Fatalf("publishing the component: %v", err)
			}
			var want []string
			for _, image := range c.scans {
				want = append(want, scan(p.SARIF, image, c.version))
			}
			if got := trivyCommands(rec.commands); !reflect.DeepEqual(got, want) {
				t.Errorf("scans:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// failingTrivy records each command and fails each Trivy scan, as
// Trivy does when it cannot download the vulnerability database.
type failingTrivy struct{ recorder }

func (f *failingTrivy) run(dir, name string, args ...string) error {
	f.recorder.run(dir, name, args...)
	if name == "trivy" {
		return errors.New("the database download failed")
	}
	return nil
}

// A failed scan does not stop the publish. It still pushes every image
// and the deploy artifact.
func TestAScanThatFailsDoesNotStopThePublish(t *testing.T) {
	p, rec, c := publishFixture(t, nil)
	runner := &failingTrivy{recorder{registry: rec.registry}}
	p.Run, p.SARIF = runner.run, t.TempDir()
	if err := p.Publish(c, "2026.10.02-001", publishRelease); err != nil {
		t.Fatalf("publishing the component: %v", err)
	}
	if last := runner.commands[len(runner.commands)-1]; last != "flux tag artifact oci://ghcr.io/liken-sh/operator-deploy:2026.10.02-001 --tag latest" {
		t.Errorf("the last command is %q, want the deploy artifact tagged latest, so the failed scan did not stop the publish", last)
	}
}

func TestAPublishedVersionIsNeverPushedAgain(t *testing.T) {
	p, rec, c := publishFixture(t, map[string][]string{
		"operator":        {"2026.10.02-001"},
		"operator-cli":    {"2026.10.02-001"},
		"operator-deploy": {"2026.10.02-001"},
	})
	if err := p.Publish(c, "2026.10.02-001", publishRelease); err != nil {
		t.Fatalf("publishing the component: %v", err)
	}
	for _, command := range rec.commands {
		if strings.Contains(command, "bake") || strings.Contains(command, "push artifact") {
			t.Errorf("the publish ran %q for a version that is already published, want no push", command)
		}
	}
}

func TestAnOlderReleaseDoesNotMoveLatest(t *testing.T) {
	p, rec, c := publishFixture(t, map[string][]string{"operator": {"2026.10.03-001"}})
	if err := p.Publish(c, "2026.10.02-001", publishRelease); err != nil {
		t.Fatalf("publishing the component: %v", err)
	}
	for _, command := range rec.commands {
		if strings.Contains(command, "operator:latest ") {
			t.Errorf("the publish ran %q, want no move of latest", command)
		}
	}
}

func TestADevelopmentBuildNeverMovesLatest(t *testing.T) {
	p, rec, c := publishFixture(t, nil)
	if err := p.Publish(c, "2026.10.02-001-dev-017-abcdef01", publishDev); err != nil {
		t.Fatalf("publishing the component: %v", err)
	}
	for _, command := range rec.commands {
		if strings.Contains(command, "latest") {
			t.Errorf("the publish ran %q, want no move of latest", command)
		}
	}
	// Two image pushes, one signature for each of the three image
	// names, and the deploy artifact.
	if len(rec.commands) != 6 {
		t.Errorf("commands:\n%s", strings.Join(rec.commands, "\n"))
	}
}

func TestAPublishRefusesAVersionOfTheWrongShape(t *testing.T) {
	p, _, c := publishFixture(t, nil)
	cases := []struct{ version, mode string }{
		{"2026.10.02-001", publishDev},
		{"2026.10.02-001-dev-017-abcdef01", publishRelease},
		{"2026.10.02-001", "nightly"},
	}
	for _, x := range cases {
		if err := p.Publish(c, x.version, x.mode); err == nil {
			t.Errorf("published %s as %s", x.version, x.mode)
		}
	}
}

func TestTheDeployArtifactSetsTheTagOfEachImagePushedWithIt(t *testing.T) {
	root := writeTree(t, map[string]string{
		"kustomization.yaml": `# The base, with a comment that survives.
resources:
  - operator.yaml
images:
  - name: ghcr.io/liken-sh/operator
    newTag: latest
  - name: ghcr.io/liken-sh/operator-sidecar
  - name: eclipse-mosquitto
    newTag: 2.0.22
`,
		"monitoring/kustomization.yaml": "resources:\n  - rules.yaml\n",
	})
	c := &Component{Outputs: Outputs{Images: []Image{{Name: "operator", Aliases: []string{"operator-sidecar"}}}}}
	c.Package.Name = "operator"
	if err := StampImages(root, c, "2026.10.02-001"); err != nil {
		t.Fatalf("stamping the images: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "kustomization.yaml"))
	want := `# The base, with a comment that survives.
resources:
  - operator.yaml
images:
  - name: ghcr.io/liken-sh/operator
    newTag: 2026.10.02-001
  - name: ghcr.io/liken-sh/operator-sidecar
    newTag: 2026.10.02-001
  - name: eclipse-mosquitto
    newTag: 2.0.22
`
	if string(got) != want {
		t.Errorf("kustomization.yaml is:\n%s\nwant:\n%s", got, want)
	}
	untouched, _ := os.ReadFile(filepath.Join(root, "monitoring/kustomization.yaml"))
	if string(untouched) != "resources:\n  - rules.yaml\n" {
		t.Errorf("monitoring/kustomization.yaml is:\n%s\nwant it unchanged", untouched)
	}
}

// pinnedPublishFixture is a pinned base image that builds on a pinned
// tool image, with the files of its recipe on disk.
func pinnedPublishFixture(t *testing.T, tags map[string][]string) (Publisher, *recorder, map[string]*Component) {
	t.Helper()
	root := writeTree(t, map[string]string{
		"tool/package.toml":      "[package]\nname = \"tool\"\nversion = \"20260928\"\nrevision = 3\n[[outputs.images]]\nname = \"tool\"\ndescription = \"The tool image.\"\n",
		"tool/Dockerfile":        "FROM debian@sha256:abc\n",
		"base/package.toml":      "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[depends]\ncomponents = [\"tool\"]\n[[outputs.images]]\nname = \"base\"\ndescription = \"The base image.\"\n",
		"base/Dockerfile":        "FROM tool\n",
		"toolchains/images.toml": fixtureImages,
	})
	return fixturePublisher(t, root, tags)
}

func TestAPinnedComponentPushesItsTagWithItsRecipeThenMovesLatest(t *testing.T) {
	p, rec, components := pinnedPublishFixture(t, map[string][]string{"base": {"20260927-1"}})
	if err := p.Publish(components["base"], "20260928-1", publishRelease); err != nil {
		t.Fatalf("publishing the component: %v", err)
	}
	recipe, err := Recipe(p.Root, components, components["base"])
	if err != nil {
		t.Fatalf("computing the recipe of the component: %v", err)
	}
	oci := []string{
		"org.opencontainers.image.created=2026-10-02T09:30:00Z",
		"org.opencontainers.image.description=The base image.",
		"org.opencontainers.image.documentation=https://github.com/liken-sh/liken/tree/0123456789abcdef0123456789abcdef01234567/base",
		"org.opencontainers.image.revision=0123456789abcdef0123456789abcdef01234567",
		"org.opencontainers.image.source=https://github.com/liken-sh/liken",
		"org.opencontainers.image.title=base",
		"org.opencontainers.image.url=https://liken.sh/",
		"org.opencontainers.image.vendor=liken",
		"org.opencontainers.image.version=20260928-1",
	}
	var labels, annotations string
	for _, kv := range oci {
		labels += " --set base.labels." + kv
		annotations += " --set base.annotations=index,manifest:" + kv
	}
	want := "docker buildx bake --file docker-bake.hcl --push" +
		labels + " --set base.labels.sh.liken.recipe=" + recipe + annotations +
		" --set base.tags=ghcr.io/liken-sh/base:20260928-1" +
		" --set base.attest=type=sbom,generator=" + fixtureScanner + " --set base.attest=type=provenance,mode=max --metadata-file <metadata> base"
	sign := signCommand("ghcr.io/liken-sh/base@" + fakeDigest("base"))
	latest := "docker buildx imagetools create --tag ghcr.io/liken-sh/base:latest ghcr.io/liken-sh/base:20260928-1"
	if !reflect.DeepEqual(rec.commands, []string{want, sign, latest}) {
		t.Errorf("commands:\n%s", strings.Join(rec.commands, "\n"))
	}
}

// A pinned image has a label for each upstream version in its
// package.toml, so a person can read from the registry which upstream
// release a dated tag contains.
func TestAPinnedImageHasALabelForEachUpstreamVersion(t *testing.T) {
	root := writeTree(t, map[string]string{
		"base/package.toml": "[package]\nname = \"base\"\nversion = \"20261004\"\nrevision = 1\n" +
			"[package.upstream]\nindi = \"2.2.5\"\nphd2 = \"2.6.14\"\n[[outputs.images]]\nname = \"base\"\ndescription = \"The base image.\"\n",
		"base/Dockerfile":        "FROM debian@sha256:abc\n",
		"toolchains/images.toml": fixtureImages,
	})
	p, rec, components := fixturePublisher(t, root, nil)
	if err := p.Publish(components["base"], "20261004-1", publishDev); err != nil {
		t.Fatalf("publishing the component: %v", err)
	}
	for _, label := range []string{" --set base.labels.sh.liken.upstream.indi=2.2.5 ", " --set base.labels.sh.liken.upstream.phd2=2.6.14 "} {
		if !strings.Contains(rec.commands[0], label) {
			t.Errorf("the build command lacks%s:\n%s", label, rec.commands[0])
		}
	}
}

// A pin that is already published is still signed and still moves
// :latest, so a run after a failed signature or a failed move finishes
// the work.
func TestAPublishedPinStillMovesLatest(t *testing.T) {
	p, rec, components := pinnedPublishFixture(t, map[string][]string{"base": {"20260928-1"}})
	if err := p.Publish(components["base"], "20260928-1", publishDev); err != nil {
		t.Fatalf("publishing the component: %v", err)
	}
	want := []string{
		signCommand("ghcr.io/liken-sh/base:20260928-1"),
		"docker buildx imagetools create --tag ghcr.io/liken-sh/base:latest ghcr.io/liken-sh/base:20260928-1",
	}
	if !reflect.DeepEqual(rec.commands, want) {
		t.Errorf("commands:\n%s", strings.Join(rec.commands, "\n"))
	}
}

func TestAPinnedComponentPublishesOnlyItsOwnTag(t *testing.T) {
	p, _, components := pinnedPublishFixture(t, nil)
	cases := []struct{ version, mode string }{
		{"2026.10.02-001", publishRelease},
		{"20260928-2", publishDev},
		{"20260928-1", "nightly"},
	}
	for _, x := range cases {
		if err := p.Publish(components["base"], x.version, x.mode); err == nil {
			t.Errorf("published %s as %s", x.version, x.mode)
		}
	}
}

// A dry run builds each image as a publish would, with the same tags
// and labels, and stamps the deploy artifact, but it pushes nothing and
// moves no tag, even for a version that is already published. It
// exports each image into the builder, where the exporter writes the
// index and its annotations and rejects any that a push would reject.
func TestADryRunBuildsWhatAPublishWouldAndPushesNothing(t *testing.T) {
	p, rec, c := publishFixture(t, map[string][]string{"operator": {"2026.10.02-001-dev-017-abcdef01"}})
	if err := p.Publish(c, "2026.10.02-001-dev-017-abcdef01", publishDry); err != nil {
		t.Fatalf("publishing the component: %v", err)
	}
	if len(rec.commands) != 2 {
		t.Fatalf("commands:\n%s", strings.Join(rec.commands, "\n"))
	}
	for i, target := range []string{"operator", "operator-cli"} {
		command := rec.commands[i]
		if !strings.HasPrefix(command, "docker buildx bake --file docker-bake.hcl --set "+target+".output=type=image,push=false --set "+target+".args.VERSION=2026.10.02-001-dev-017-abcdef01") ||
			!strings.HasSuffix(command, " "+target) || strings.Contains(command, "--push") {
			t.Errorf("the build of %s is %q, want a build that does not push", target, command)
		}
	}
}

func TestADryRunRefusesAVersionOfTheWrongShape(t *testing.T) {
	p, _, c := publishFixture(t, nil)
	if err := p.Publish(c, "check", publishDry); err == nil {
		t.Error("a dry run accepted the version \"check\", want an error")
	}
}

// A dry run does what a publish of the same tag would do, and pushes
// nothing. A pinned tag that is not published yet is built. A published
// one would only be signed again, so its dry run builds nothing and
// never contacts the base's upstream servers.
func TestADryRunOfAPinnedComponentBuildsOnlyATagThatIsNotPublished(t *testing.T) {
	cases := []struct {
		name   string
		tags   []string
		builds bool
	}{
		{"not published", []string{"20260927-1"}, true},
		{"published", []string{"20260928-1"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, rec, components := pinnedPublishFixture(t, map[string][]string{"base": c.tags})
			if err := p.Publish(components["base"], "20260928-1", publishDry); err != nil {
				t.Fatalf("publishing the component: %v", err)
			}
			built := len(rec.commands) == 1 && !strings.Contains(rec.commands[0], "--push") &&
				strings.Contains(rec.commands[0], "--set base.labels.sh.liken.recipe=sha256:")
			if built != c.builds || len(rec.commands) > 1 {
				t.Errorf("commands:\n%s", strings.Join(rec.commands, "\n"))
			}
		})
	}
}

// When the plan job wrote the override file, the publish builds on the
// published bases that the job checked. Without the file, it builds on
// the bases in the tree.
func TestThePublishReadsTheOverrideFileWhenItIsThere(t *testing.T) {
	p, rec, components := pinnedPublishFixture(t, map[string][]string{"base": {"20260927-1"}})
	if err := os.WriteFile(filepath.Join(p.Root, overrideFile), []byte("# no bases\n"), 0o644); err != nil {
		t.Fatalf("writing a fixture file: %v", err)
	}
	if err := p.Publish(components["base"], "20260928-1", publishRelease); err != nil {
		t.Fatalf("publishing the component: %v", err)
	}
	if !strings.HasPrefix(rec.commands[0], "docker buildx bake --file docker-bake.hcl --file docker-bake.override.hcl --push ") {
		t.Errorf("the build command is %q, want it to read the override file", rec.commands[0])
	}
}
