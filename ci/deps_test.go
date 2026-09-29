package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// depsFixture is a repository whose tools report fixed answers: an
// operator whose Go module uses brand and base, whose docs module uses
// brand, whose Cargo workspace takes a crate of base by path and one of
// the project's crates from git, and whose Dockerfile starts from
// base's image.
func depsFixture(t *testing.T, operatorDepends string) DepsChecker {
	t.Helper()
	root := writeTree(t, map[string]string{
		"brand/package.toml": "[package]\nname = \"brand\"\n",
		"base/package.toml":  "[package]\nname = \"base\"\n[[outputs.images]]\nname = \"base-image\"\n",
		"base/Dockerfile":    "FROM scratch\n",
		"operator/package.toml": "[package]\nname = \"operator\"\n[depends]\ncomponents = [" + operatorDepends + "]\n" +
			"[[outputs.images]]\nname = \"operator\"\ncontexts = { brand = \"brand\" }\n" +
			"[[outputs.images]]\nname = \"operator-sidecar\"\ncontexts = { brand = \"brand\" }\n",
		"operator/go.mod":          "module x\n",
		"operator/docs/go.mod":     "module x/docs\n",
		"operator/Cargo.lock":      "",
		"operator/Dockerfile":      "ARG BASE=ghcr.io/liken-sh/base-image:2026.09.08-001\nFROM golang AS build\nFROM ${BASE}\nFROM ghcr.io/liken-sh/corrosion:1\nFROM base-image AS runtime\nCOPY --from=brand fonts /f\n",
		"operator/testdata/go.mod": "module ignored\n",
	})
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	return DepsChecker{
		Root:       root,
		Components: components,
		GoList: func(dir string) ([]goModule, error) {
			switch filepath.Base(dir) {
			case "docs":
				return []goModule{{Path: "x/docs", Dir: dir}, {Path: "github.com/liken-sh/brand", Dir: filepath.Join(abs, "brand")}}, nil
			case "operator":
				return []goModule{
					{Path: "x", Dir: dir},
					{Path: "github.com/liken-sh/brand", Dir: filepath.Join(abs, "brand")},
					{Path: "github.com/liken-sh/base", Dir: filepath.Join(abs, "base")},
					{Path: "k8s.io/client-go", Dir: "/home/x/go/pkg/mod/k8s.io/client-go@v0.36.3"},
				}, nil
			}
			t.Fatalf("go list ran in %s", dir)
			return nil, nil
		},
		CargoMetadata: func(dir string) ([]cargoPackage, error) {
			p := cargoPackage{Name: "browser", ManifestPath: filepath.Join(abs, "operator/Cargo.toml")}
			p.Dependencies = append(p.Dependencies,
				struct {
					Name   string `json:"name"`
					Source string `json:"source"`
					Path   string `json:"path"`
				}{Name: "base-crate", Path: filepath.Join(abs, "base/crate")},
				struct {
					Name   string `json:"name"`
					Source string `json:"source"`
					Path   string `json:"path"`
				}{Name: "serde", Source: "registry+https://github.com/rust-lang/crates.io-index"},
			)
			return []cargoPackage{p}, nil
		},
	}
}

func TestEveryUseIsDeclared(t *testing.T) {
	findings, err := depsFixture(t, `"brand", "base"`).Check()
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("findings %v", findings)
	}
}

func TestAnUndeclaredUseIsFound(t *testing.T) {
	findings, err := depsFixture(t, "").Check()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range findings {
		got = append(got, f.String())
	}
	want := []string{
		"operator uses brand, and its package.toml does not name it in [depends]: operator/go.mod imports github.com/liken-sh/brand",
		"operator uses base, and its package.toml does not name it in [depends]: operator/go.mod imports github.com/liken-sh/base",
		"operator uses base, and its package.toml does not name it in [depends]: operator/Cargo.toml depends on the crate base-crate",
		"operator uses brand, and its package.toml does not name it in [depends]: the image operator takes brand as its build context brand",
		"operator uses base, and its package.toml does not name it in [depends]: operator/Dockerfile builds on the image base-image",
		"operator uses base, and its package.toml does not name it in [depends]: operator/Dockerfile starts from ghcr.io/liken-sh/base-image",
		"operator uses brand, and its package.toml does not name it in [depends]: the image operator-sidecar takes brand as its build context brand",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings:\n%s", strings.Join(got, "\n"))
	}
}

func TestAProjectModuleFromTheProxyIsFound(t *testing.T) {
	d := depsFixture(t, `"brand", "base"`)
	d.GoList = func(dir string) ([]goModule, error) {
		return []goModule{{Path: "github.com/liken-sh/brand", Dir: "/home/x/go/pkg/mod/github.com/liken-sh/brand@v0.0.0"}}, nil
	}
	findings, err := d.Check()
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || !strings.Contains(findings[0].String(), "reads github.com/liken-sh/brand from the module proxy") {
		t.Errorf("findings %v", findings)
	}
}

func TestAProjectCrateFromGitIsFound(t *testing.T) {
	d := depsFixture(t, `"brand", "base"`)
	d.CargoMetadata = func(dir string) ([]cargoPackage, error) {
		p := cargoPackage{Name: "browser", ManifestPath: filepath.Join(dir, "Cargo.toml")}
		p.Dependencies = append(p.Dependencies, struct {
			Name   string `json:"name"`
			Source string `json:"source"`
			Path   string `json:"path"`
		}{Name: "media-screen", Source: "git+https://github.com/liken-sh/media-operator?rev=abc"})
		return []cargoPackage{p}, nil
	}
	findings, err := d.Check()
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0].String(), "use a path dependency") {
		t.Errorf("findings %v", findings)
	}
}

func TestANestedComponentOwnsItsOwnDirectory(t *testing.T) {
	root := writeTree(t, map[string]string{
		"liken/package.toml":        "[package]\nname = \"liken\"\n",
		"liken/kernel/package.toml": "[package]\nname = \"kernel\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	d := DepsChecker{Root: root, Components: components}
	cases := map[string]string{"liken/init": "liken", "liken/kernel/config": "kernel", "liken": "liken", "plans": "", "likenx": ""}
	for path, want := range cases {
		if got := d.owner(path); got != want {
			t.Errorf("owner(%s) = %q, want %q", path, got, want)
		}
	}
}
