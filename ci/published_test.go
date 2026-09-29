package main

import (
	"reflect"
	"testing"
)

func TestADeployArtifactWithAReleaseNamesTheVersions(t *testing.T) {
	root := writeTree(t, map[string]string{
		"operator/package.toml": "[package]\nname = \"operator\"\n[outputs]\ndeploy = \"deploy\"\n[[outputs.images]]\nname = \"operator\"\n",
		"crd/package.toml":      "[package]\nname = \"crd\"\n[outputs]\ndeploy = \"deploy\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	registry := &fakeRegistry{tags: map[string][]string{
		"operator":        {"2026.09.27-001"},
		"operator-deploy": {"2026.09.29-001", "buildcache"},
		"crd-deploy":      {"buildcache"},
	}}
	published := Published{Registry: registry.serve(t)}
	cases := map[string][]string{
		"operator": {"2026.09.29-001", "buildcache"},
		"crd":      {"buildcache"},
	}
	for name, want := range cases {
		got, err := published.Versions(components[name])
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, %v", name, got, err)
		}
	}
}
