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
	want := []string{
		"docker buildx build --push --file operator/Dockerfile --build-arg VERSION=2026.10.02-001 --cache-from type=registry,ref=ghcr.io/liken-sh/operator:buildcache --label org.opencontainers.image.source=https://github.com/liken-sh/liken --label org.opencontainers.image.revision=0123456789abcdef0123456789abcdef01234567 --label org.opencontainers.image.version=2026.10.02-001 --platform linux/amd64 --target operator --build-context brand=brand --tag ghcr.io/liken-sh/operator:2026.10.02-001 --tag ghcr.io/liken-sh/operator-sidecar:2026.10.02-001 operator",
		"docker buildx imagetools create --tag ghcr.io/liken-sh/operator:latest ghcr.io/liken-sh/operator:2026.10.02-001",
		"docker buildx imagetools create --tag ghcr.io/liken-sh/operator-sidecar:latest ghcr.io/liken-sh/operator:2026.10.02-001",
		"docker buildx build --push --file operator/Dockerfile.cli --build-arg VERSION=2026.10.02-001 --cache-from type=registry,ref=ghcr.io/liken-sh/operator-cli:buildcache --label org.opencontainers.image.source=https://github.com/liken-sh/liken --label org.opencontainers.image.revision=0123456789abcdef0123456789abcdef01234567 --label org.opencontainers.image.version=2026.10.02-001 --platform linux/amd64,linux/arm64 --tag ghcr.io/liken-sh/operator-cli:2026.10.02-001 operator",
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
		if strings.Contains(command, "build --push") || strings.Contains(command, "push artifact") {
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
