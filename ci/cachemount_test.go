package main

import (
	"reflect"
	"strings"
	"testing"
)

func cacheMountTree(t *testing.T, dockerfile string) string {
	return writeTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[[outputs.images]]\nname = \"app\"\ncontexts = { brand = \"brand\" }\ncache-mount = true\n",
		"app/Dockerfile":   dockerfile,
	})
}

func TestTheBakeFileCopiesAnImagesCacheMountInAndOut(t *testing.T) {
	root := cacheMountTree(t, "FROM scratch AS cache-seed\nFROM scratch AS cache-export\nFROM scratch\n")
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Bake(root, components)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{
		"targets = [\n    \"app\",\n  ]",
		`target "app-cache-seed" {
  context    = "app"
  dockerfile = "Dockerfile"
  target     = "cache-seed"
  platforms  = ["linux/amd64"]
  contexts = {
    "brand" = "brand"
    "cache" = ".image-cache/app"
  }
  output = ["type=cacheonly"]
}`,
		`target "app-cache-export" {
  context    = "app"
  dockerfile = "Dockerfile"
  target     = "cache-export"
  platforms  = ["linux/amd64"]
  contexts = {
    "brand" = "brand"
  }
  output = ["type=local,dest=.image-cache/app"]
}`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the bake file lacks\n%s\n\nit is\n%s", want, text)
		}
	}
}

func TestTheBakeFileNeedsTheCacheStagesOfACacheMount(t *testing.T) {
	root := cacheMountTree(t, "FROM scratch AS cache-seed\nFROM scratch\n")
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Bake(root, components)
	if err == nil || !strings.Contains(err.Error(), "has no stage cache-export") {
		t.Fatalf("Bake answered %v, want the missing stage named", err)
	}
}

func TestTheImagesMatrixMarksAnImageWithACacheMount(t *testing.T) {
	root := cacheMountTree(t, "FROM scratch AS cache-seed\nFROM scratch AS cache-export\nFROM scratch\n")
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	_, images := everything(components["app"])
	if want := []ImageRun{{Image: "app", Load: true, Cache: true}}; !reflect.DeepEqual(images, want) {
		t.Errorf("the matrix is %+v, want %+v", images, want)
	}
}
