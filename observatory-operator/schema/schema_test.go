package schema

// The generator writes the CRDs of the kinds with procedures and the
// Go types in observatory/ from the files in this directory and the
// standard properties in indi/standard/. This test runs the generator
// and fails when a written file differs from its output, or when
// observatory/ has a generated_*.go file that the generator does not
// write. `make schema` runs it with -update to write the files.

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
		t.Fatalf("generating the schema outputs: %v", err)
	}
	if *update {
		for path, data := range out {
			if err := os.WriteFile(filepath.Join("..", path), data, 0o644); err != nil {
				t.Fatalf("writing %s: %v", path, err)
			}
		}
	}
	for _, path := range sortedKeys(out) {
		got, err := os.ReadFile(filepath.Join("..", path))
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if string(got) != string(out[path]) {
			t.Errorf("%s differs from the generator output for schema/ and indi/standard/, run make schema", path)
		}
	}
	generated, err := filepath.Glob(filepath.Join("..", "observatory", "generated_*.go"))
	if err != nil {
		t.Fatalf("listing the generated types: %v", err)
	}
	for _, path := range generated {
		rel, _ := filepath.Rel("..", path)
		if _, written := out[rel]; !written {
			t.Errorf("%s is not an output of the generator, remove it", rel)
		}
	}
}
