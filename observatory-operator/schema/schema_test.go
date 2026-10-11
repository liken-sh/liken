package schema

// The CRDs of the kinds with procedures and the generated Go types in
// observatory/ come from the files in this directory and from the
// standard properties in indi/standard/. This test generates them
// again and fails when a result differs from the file, or when
// observatory/ holds a generated file that the generator no longer
// writes. `make schema` runs it with -update, which writes them.

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "write the CRDs in deploy/ and the generated types in observatory/")

func TestTheSchemaIsCurrent(t *testing.T) {
	out, err := generate("..")
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		for path, data := range out {
			if err := os.WriteFile(filepath.Join("..", path), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, path := range sortedKeys(out) {
		got, err := os.ReadFile(filepath.Join("..", path))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(out[path]) {
			t.Errorf("%s is stale against schema/ and indi/standard/; run make schema", path)
		}
	}
	generated, err := filepath.Glob(filepath.Join("..", "observatory", "generated_*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range generated {
		rel, _ := filepath.Rel("..", path)
		if _, written := out[rel]; !written {
			t.Errorf("%s is no output of the generator; remove it", rel)
		}
	}
}
