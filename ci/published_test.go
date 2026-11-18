package main

import (
	"reflect"
	"testing"
)

func TestTheVersionsOfADeployArtifactWithAReleaseAreItsTags(t *testing.T) {
	root := writeTree(t, map[string]string{
		"operator/package.toml": "[package]\nname = \"operator\"\n[outputs]\ndeploy = \"deploy\"\n[[outputs.images]]\nname = \"operator\"\ndescription = \"The operator image.\"\n",
		"crd/package.toml":      "[package]\nname = \"crd\"\n[outputs]\ndeploy = \"deploy\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
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
			t.Errorf("the versions of %s are %v with error %v, want the listed tags", name, got, err)
		}
	}
}
