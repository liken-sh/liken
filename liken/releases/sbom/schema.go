package main

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The JSON schema of the CycloneDX specification, which
// schema/latest.sh pins. Every SBOM that this program writes passes
// the schema before the program writes the file, so a release never
// carries an SBOM that a reader of CycloneDX refuses.
//
//go:embed schema/*.json
var schemaFiles embed.FS

// schemaBase is the URL that each schema file names as its $id. The
// BOM schema names the other three by a $ref relative to its own $id,
// so the compiler finds them under the same base.
const schemaBase = "http://cyclonedx.org/schema/"

// specVersion is the CycloneDX version that the SBOM states, the minor
// version in the name of the one BOM schema in schema/.
var specVersion = func() string {
	matches, err := fs.Glob(schemaFiles, "schema/bom-*.schema.json")
	if err != nil || len(matches) != 1 {
		panic(fmt.Sprintf("schema/ must hold one bom-<version>.schema.json, and it holds %v", matches))
	}
	return strings.TrimSuffix(strings.TrimPrefix(path.Base(matches[0]), "bom-"), ".schema.json")
}()

// bomSchema is the compiled BOM schema.
var bomSchema = func() *jsonschema.Schema {
	c := jsonschema.NewCompiler()
	entries, err := fs.ReadDir(schemaFiles, "schema")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		data, err := schemaFiles.ReadFile("schema/" + e.Name())
		if err != nil {
			panic(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			panic(fmt.Sprintf("schema/%s: %v", e.Name(), err))
		}
		if err := c.AddResource(schemaBase+e.Name(), doc); err != nil {
			panic(err)
		}
	}
	return c.MustCompile(schemaBase + "bom-" + specVersion + ".schema.json")
}()

// validate checks a document against the BOM schema, and its error
// names each field that fails.
func validate(data []byte) error {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("the SBOM is not JSON: %w", err)
	}
	if err := bomSchema.Validate(doc); err != nil {
		return fmt.Errorf("the SBOM does not follow CycloneDX %s: %w", specVersion, err)
	}
	return nil
}
