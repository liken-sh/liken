package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// recorder is a Runner that records each command and runs nothing.
// For a bake with --metadata-file, it writes the file that buildx
// writes, with the digest that fakeDigest gives the target, which is
// the bake's last argument. The file's path is a new temporary path
// in each publish, so the recorded command names it <metadata>.
type recorder struct{ commands []string }

func (r *recorder) run(dir, name string, args ...string) error {
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
	return os.WriteFile(path, data, 0o644)
}

// fixtureDockerConfig is the Docker configuration directory of the
// publish fixtures, where docker/login-action writes the credential
// that cosign pushes each signature with.
const fixtureDockerConfig = "/home/runner/.docker"

// fakeDigest is the digest of the image that the recorder's bake
// pushes for a target.
func fakeDigest(target string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(target)))
}

// fixtureCosign is the cosign image of the publish fixtures'
// images.toml.
const fixtureCosign = "ghcr.io/sigstore/cosign/cosign:v3.1.4@sha256:0f06154836d2de34f887a5c273506d52ca43edaa1d9e53e6510fa78a48f69c2d"

// fixtureScanner is the SBOM generator of the publish fixtures'
// images.toml.
const fixtureScanner = "docker/buildkit-syft-scanner:1.12.1@sha256:873a7458c3c8ac8e32c7b796c10e1600b07b963d2aa000a72a2d97aaaa2d6315"

const fixtureImages = "[build]\ncosign = \"" + fixtureCosign + "\"\nbuildkit-syft-scanner = \"" + fixtureScanner + "\"\n"

func publishFixture(t *testing.T, tags map[string][]string) (Publisher, *recorder, *Component) {
	t.Helper()
	root := writeTree(t, map[string]string{
		"operator/package.toml": `[package]
name = "operator"
[outputs]
deploy = "deploy"
[[outputs.images]]
name = "operator"
target = "operator"
aliases = ["operator-sidecar"]
contexts = { brand = "brand" }
[[outputs.images]]
name = "operator-cli"
file = "Dockerfile.cli"
platforms = ["linux/amd64", "linux/arm64"]
`,
		"operator/deploy/kustomization.yaml": `# The base.
images:
  - name: ghcr.io/liken-sh/operator
    newTag: latest
`,
		"toolchains/images.toml": fixtureImages,
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	registry := (&fakeRegistry{tags: tags}).serve(t)
	return Publisher{Root: root, Registry: registry, Run: rec.run, Commit: "0123456789abcdef0123456789abcdef01234567", DockerConfig: fixtureDockerConfig}, rec, components["operator"]
}

func TestAReleasePushesEveryImageThenTheDeployArtifactThenLatest(t *testing.T) {
	p, rec, c := publishFixture(t, map[string][]string{"operator": {"2026.09.27-001"}})
	if err := p.Publish(c, "2026.10.02-001", publishRelease); err != nil {
		t.Fatal(err)
	}
	labels := func(target string) string {
		return " --set " + target + ".labels.org.opencontainers.image.revision=0123456789abcdef0123456789abcdef01234567" +
			" --set " + target + ".labels.org.opencontainers.image.source=https://github.com/liken-sh/liken" +
			" --set " + target + ".labels.org.opencontainers.image.version=2026.10.02-001"
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
		t.Fatalf("commands:\n%s", strings.Join(rec.commands, "\n"))
	}
	rest := rec.commands[len(want):]
	if len(rest) != 2 || !strings.HasPrefix(rest[0], "flux push artifact oci://ghcr.io/liken-sh/operator-deploy:2026.10.02-001 --path ") ||
		!strings.HasSuffix(rest[0], "--source https://github.com/liken-sh/liken --revision 2026.10.02-001@sha1:0123456789abcdef0123456789abcdef01234567") ||
		rest[1] != "flux tag artifact oci://ghcr.io/liken-sh/operator-deploy:2026.10.02-001 --tag latest" {
		t.Errorf("the deploy commands:\n%s", strings.Join(rest, "\n"))
	}
}

// trivyCommands lists the Trivy scans among the recorded commands.
func trivyCommands(commands []string) []string {
	var scans []string
	for _, command := range commands {
		if strings.HasPrefix(command, "trivy ") {
			scans = append(scans, command)
		}
	}
	return scans
}

// The publish scans each image right after it pushes it, and only an
// image that it pushes: a dry run pushes nothing, and a published
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
				t.Fatal(err)
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

// failingTrivy records each command and fails each Trivy scan, as a
// scan does when the vulnerability database cannot be downloaded.
type failingTrivy struct{ recorder }

func (f *failingTrivy) run(dir, name string, args ...string) error {
	f.recorder.run(dir, name, args...)
	if name == "trivy" {
		return errors.New("the database download failed")
	}
	return nil
}

// A scan reports and does not gate: a scan that fails leaves the
// publish to push every image and the deploy artifact.
func TestAScanThatFailsDoesNotStopThePublish(t *testing.T) {
	p, _, c := publishFixture(t, nil)
	runner := &failingTrivy{}
	p.Run, p.SARIF = runner.run, t.TempDir()
	if err := p.Publish(c, "2026.10.02-001", publishRelease); err != nil {
		t.Fatal(err)
	}
	if last := runner.commands[len(runner.commands)-1]; last != "flux tag artifact oci://ghcr.io/liken-sh/operator-deploy:2026.10.02-001 --tag latest" {
		t.Errorf("the publish stopped at %q", last)
	}
}

func TestAPublishedVersionIsNeverPushedAgain(t *testing.T) {
	p, rec, c := publishFixture(t, map[string][]string{
		"operator":        {"2026.10.02-001"},
		"operator-cli":    {"2026.10.02-001"},
		"operator-deploy": {"2026.10.02-001"},
	})
	if err := p.Publish(c, "2026.10.02-001", publishRelease); err != nil {
		t.Fatal(err)
	}
	for _, command := range rec.commands {
		if strings.Contains(command, "bake") || strings.Contains(command, "push artifact") {
			t.Errorf("pushed again: %s", command)
		}
	}
}

func TestAnOlderReleaseDoesNotMoveLatest(t *testing.T) {
	p, rec, c := publishFixture(t, map[string][]string{"operator": {"2026.10.03-001"}})
	if err := p.Publish(c, "2026.10.02-001", publishRelease); err != nil {
		t.Fatal(err)
	}
	for _, command := range rec.commands {
		if strings.Contains(command, "operator:latest ") {
			t.Errorf("moved latest: %s", command)
		}
	}
}

func TestADevelopmentBuildNeverMovesLatest(t *testing.T) {
	p, rec, c := publishFixture(t, nil)
	if err := p.Publish(c, "2026.10.02-001-dev-017-abcdef01", publishDev); err != nil {
		t.Fatal(err)
	}
	for _, command := range rec.commands {
		if strings.Contains(command, "latest") {
			t.Errorf("moved latest: %s", command)
		}
	}
	// Two pushes, a signature for each of the three image names, and
	// the deploy artifact.
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

func TestTheDeployArtifactNamesTheImagesPushedWithIt(t *testing.T) {
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
		t.Fatal(err)
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
		t.Errorf("kustomization.yaml:\n%s", got)
	}
	untouched, _ := os.ReadFile(filepath.Join(root, "monitoring/kustomization.yaml"))
	if string(untouched) != "resources:\n  - rules.yaml\n" {
		t.Errorf("monitoring/kustomization.yaml:\n%s", untouched)
	}
}

// pinnedPublishFixture is a pinned base on a pinned tool, with its
// recipe's inputs on disk.
func pinnedPublishFixture(t *testing.T, tags map[string][]string) (Publisher, *recorder, map[string]*Component) {
	t.Helper()
	root := writeTree(t, map[string]string{
		"tool/package.toml":      "[package]\nname = \"tool\"\nversion = \"20260928\"\nrevision = 3\n[[outputs.images]]\nname = \"tool\"\n",
		"tool/Dockerfile":        "FROM debian@sha256:abc\n",
		"base/package.toml":      "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[depends]\ncomponents = [\"tool\"]\n[[outputs.images]]\nname = \"base\"\n",
		"base/Dockerfile":        "FROM tool\n",
		"toolchains/images.toml": fixtureImages,
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	registry := (&fakeRegistry{tags: tags}).serve(t)
	return Publisher{Root: root, Registry: registry, Run: rec.run, Commit: "0123456789abcdef0123456789abcdef01234567", Components: components, DockerConfig: fixtureDockerConfig}, rec, components
}

func TestAPinnedComponentPushesItsTagWithItsRecipeThenMovesLatest(t *testing.T) {
	p, rec, components := pinnedPublishFixture(t, map[string][]string{"base": {"20260927-1"}})
	if err := p.Publish(components["base"], "20260928-1", publishRelease); err != nil {
		t.Fatal(err)
	}
	recipe, err := Recipe(p.Root, components, components["base"])
	if err != nil {
		t.Fatal(err)
	}
	want := "docker buildx bake --file docker-bake.hcl --push" +
		" --set base.labels.org.opencontainers.image.revision=0123456789abcdef0123456789abcdef01234567" +
		" --set base.labels.org.opencontainers.image.source=https://github.com/liken-sh/liken" +
		" --set base.labels.org.opencontainers.image.version=20260928-1" +
		" --set base.labels.sh.liken.recipe=" + recipe +
		" --set base.tags=ghcr.io/liken-sh/base:20260928-1" +
		" --set base.attest=type=sbom,generator=" + fixtureScanner + " --set base.attest=type=provenance,mode=max --metadata-file <metadata> base"
	sign := signCommand("ghcr.io/liken-sh/base@" + fakeDigest("base"))
	latest := "docker buildx imagetools create --tag ghcr.io/liken-sh/base:latest ghcr.io/liken-sh/base:20260928-1"
	if !reflect.DeepEqual(rec.commands, []string{want, sign, latest}) {
		t.Errorf("commands:\n%s", strings.Join(rec.commands, "\n"))
	}
}

// A pinned image carries a label for each upstream version that its
// package.toml states, so a person reads from the registry which
// release of the upstream project a dated tag holds.
func TestAPinnedImageCarriesItsUpstreamVersionsAsLabels(t *testing.T) {
	root := writeTree(t, map[string]string{
		"base/package.toml": "[package]\nname = \"base\"\nversion = \"20261004\"\nrevision = 1\n" +
			"[package.upstream]\nindi = \"2.2.5\"\nphd2 = \"2.6.14\"\n[[outputs.images]]\nname = \"base\"\n",
		"base/Dockerfile":        "FROM debian@sha256:abc\n",
		"toolchains/images.toml": fixtureImages,
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	p := Publisher{Root: root, Registry: (&fakeRegistry{}).serve(t), Run: rec.run, Commit: "0123456789abcdef0123456789abcdef01234567", Components: components, DockerConfig: fixtureDockerConfig}
	if err := p.Publish(components["base"], "20261004-1", publishDev); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{" --set base.labels.sh.liken.upstream.indi=2.2.5 ", " --set base.labels.sh.liken.upstream.phd2=2.6.14 "} {
		if !strings.Contains(rec.commands[0], label) {
			t.Errorf("the build has no%s:\n%s", label, rec.commands[0])
		}
	}
}

// A pin that is already published is still signed and still moves
// :latest, so a run again after a failed signature or a failed move
// does it.
func TestAPublishedPinStillMovesLatest(t *testing.T) {
	p, rec, components := pinnedPublishFixture(t, map[string][]string{"base": {"20260928-1"}})
	if err := p.Publish(components["base"], "20260928-1", publishDev); err != nil {
		t.Fatal(err)
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

// A dry run builds each image the way a publish would, with the same
// tags and labels, and stamps the deploy artifact, but it pushes
// nothing and moves no tag, even for a version that is published.
func TestADryRunBuildsWhatAPublishWouldAndPushesNothing(t *testing.T) {
	p, rec, c := publishFixture(t, map[string][]string{"operator": {"2026.10.02-001-dev-017-abcdef01"}})
	if err := p.Publish(c, "2026.10.02-001-dev-017-abcdef01", publishDry); err != nil {
		t.Fatal(err)
	}
	if len(rec.commands) != 2 {
		t.Fatalf("commands:\n%s", strings.Join(rec.commands, "\n"))
	}
	for i, target := range []string{"operator", "operator-cli"} {
		command := rec.commands[i]
		if !strings.HasPrefix(command, "docker buildx bake --file docker-bake.hcl --set "+target+".args.VERSION=2026.10.02-001-dev-017-abcdef01") ||
			!strings.HasSuffix(command, " "+target) || strings.Contains(command, "--push") {
			t.Errorf("the build of %s: %s", target, command)
		}
	}
}

func TestADryRunRefusesAVersionOfTheWrongShape(t *testing.T) {
	p, _, c := publishFixture(t, nil)
	if err := p.Publish(c, "check", publishDry); err == nil {
		t.Error("a dry run took a version that no publish takes")
	}
}

func TestADryRunOfAPinnedComponentBuildsItsTagAndPushesNothing(t *testing.T) {
	p, rec, components := pinnedPublishFixture(t, map[string][]string{"base": {"20260928-1"}})
	if err := p.Publish(components["base"], "20260928-1", publishDry); err != nil {
		t.Fatal(err)
	}
	if len(rec.commands) != 1 || strings.Contains(rec.commands[0], "--push") ||
		!strings.Contains(rec.commands[0], "--set base.labels.sh.liken.recipe=sha256:") {
		t.Errorf("commands:\n%s", strings.Join(rec.commands, "\n"))
	}
}
