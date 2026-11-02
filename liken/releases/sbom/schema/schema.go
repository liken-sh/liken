// Package schema checks a document against the JSON schema of the
// CycloneDX specification, which latest.sh in this directory pins.
//
// Two programs read it. releases/sbom checks every SBOM that it writes
// before it writes the file, so a release never carries an SBOM that a
// reader of CycloneDX refuses. `liken verify release` checks the SBOM
// that a person downloads, so the person learns that the published
// file still follows the schema. One copy of the schema serves both.
package schema

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed *.json
var files embed.FS

// base is the URL that each schema file names as its $id. The BOM
// schema names the other three by a $ref relative to its own $id, so
// the compiler finds them under the same base.
const base = "http://cyclonedx.org/schema/"

// SpecVersion is the CycloneDX version that an SBOM states, the minor
// version in the name of the one BOM schema in this directory.
var SpecVersion = func() string {
	matches, err := fs.Glob(files, "bom-*.schema.json")
	if err != nil || len(matches) != 1 {
		panic(fmt.Sprintf("the schema directory must hold one bom-<version>.schema.json, and it holds %v", matches))
	}
	return strings.TrimSuffix(strings.TrimPrefix(matches[0], "bom-"), ".schema.json")
}()

// URL is the $id of the BOM schema, which an SBOM names as its
// $schema.
var URL = base + "bom-" + SpecVersion + ".schema.json"

// bomSchema is the compiled BOM schema.
var bomSchema = func() *jsonschema.Schema {
	c := jsonschema.NewCompiler()
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		data, err := files.ReadFile(e.Name())
		if err != nil {
			panic(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			panic(fmt.Sprintf("%s: %v", e.Name(), err))
		}
		if err := c.AddResource(base+e.Name(), doc); err != nil {
			panic(err)
		}
	}
	return c.MustCompile(URL)
}()

// Validate checks a document against the BOM schema, and its error
// names each field that fails.
func Validate(data []byte) error {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("the SBOM is not JSON: %w", err)
	}
	if err := bomSchema.Validate(doc); err != nil {
		return fmt.Errorf("the SBOM does not follow CycloneDX %s: %w", SpecVersion, err)
	}
	return nil
}
