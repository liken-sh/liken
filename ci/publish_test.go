package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// recorder is a Runner that records each command and runs nothing.
type recorder struct{ commands []string }

func (r *recorder) run(dir, name string, args ...string) error {
	r.commands = append(r.commands, name+" "+strings.Join(args, " "))
	return nil
}

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
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	registry := (&fakeRegistry{tags: tags}).serve(t)
	return Publisher{Root: root, Registry: registry, Run: rec.run, Commit: "0123456789abcdef0123456789abcdef01234567"}, rec, components["operator"]
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
	want := []string{
		"docker buildx bake --file docker-bake.hcl --push --set operator.args.VERSION=2026.10.02-001" + labels("operator") +
			" --set operator.tags=ghcr.io/liken-sh/operator:2026.10.02-001 --set operator.tags=ghcr.io/liken-sh/operator-sidecar:2026.10.02-001 operator",
		"docker buildx imagetools create --tag ghcr.io/liken-sh/operator:latest ghcr.io/liken-sh/operator:2026.10.02-001",
		"docker buildx imagetools create --tag ghcr.io/liken-sh/operator-sidecar:latest ghcr.io/liken-sh/operator:2026.10.02-001",
		"docker buildx bake --file docker-bake.hcl --push --set operator-cli.args.VERSION=2026.10.02-001" + labels("operator-cli") +
			" --set operator-cli.tags=ghcr.io/liken-sh/operator-cli:2026.10.02-001 operator-cli",
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
	if len(rec.commands) != 3 {
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
		"tool/package.toml": "[package]\nname = \"tool\"\nversion = \"20260928\"\nrevision = 3\n[[outputs.images]]\nname = \"tool\"\n",
		"tool/Dockerfile":   "FROM debian@sha256:abc\n",
		"base/package.toml": "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[depends]\ncomponents = [\"tool\"]\n[[outputs.images]]\nname = \"base\"\n",
		"base/Dockerfile":   "FROM tool\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	registry := (&fakeRegistry{tags: tags}).serve(t)
	return Publisher{Root: root, Registry: registry, Run: rec.run, Commit: "0123456789abcdef0123456789abcdef01234567", Components: components}, rec, components
}

func TestAPinnedComponentPushesItsTagWithItsRecipeAndLeavesLatest(t *testing.T) {
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
		" --set base.tags=ghcr.io/liken-sh/base:20260928-1 base"
	if !reflect.DeepEqual(rec.commands, []string{want}) {
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
