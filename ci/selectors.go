package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// A DeviceClass selector is a CEL expression that the scheduler runs on
// every device of every driver in the cluster. A device that lacks an
// attribute fails a selector that reads it with a runtime error, "no
// such key", and the scheduler then fails each claim of the class on
// that node, not only the claim of that device. The devices of one
// driver differ in their attributes: a misc device such as
// /dev/vhost-net has no bus and no bound driver. So each read of an
// attribute or a capacity in a class needs a has() of it earlier in the
// selector, which CEL's && reads first, or the optional form .? with
// orValue(). checkSelectors finds each read that has neither.
//
// The check reads only DeviceClasses. The scheduler runs a claim's own
// selectors only on the devices that its class selected, so a class
// that guards its reads keeps a claim's reads of the same attributes
// safe.

// attributeRead matches one read of a device's attribute or capacity
// by its qualified name, with any has( before it.
var attributeRead = regexp.MustCompile(`(has\(\s*)?(device\.(?:attributes|capacity)\["[^"]+"\]\.[A-Za-z_][A-Za-z0-9_]*)`)

// unguardedReads lists each read in the expression that no has() of
// the same name precedes, in the order of the expression.
func unguardedReads(expression string) []string {
	guarded := map[string]bool{}
	var reads []string
	for _, m := range attributeRead.FindAllStringSubmatch(expression, -1) {
		name := m[2]
		switch {
		case m[1] != "":
			guarded[name] = true
		case !guarded[name]:
			reads = append(reads, name)
			guarded[name] = true
		}
	}
	return reads
}

// checkSelectors lists each read with no guard in each DeviceClass of
// the repository: the YAML files, and the YAML blocks of the pages and
// the skills, which a reader copies a class from. A plan and test data
// keep what they were written with.
func checkSelectors(root string) ([]string, error) {
	files, err := sourceFiles(root)
	if err != nil {
		return nil, err
	}
	var findings []string
	for _, file := range files {
		ext := path.Ext(file)
		if ext != ".yaml" && ext != ".yml" && ext != ".md" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, file))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		documents := []string{string(data)}
		if ext == ".md" {
			documents = yamlBlocks(string(data))
		}
		for _, document := range documents {
			for _, class := range classSelectors(document) {
				for _, read := range unguardedReads(class.expression) {
					findings = append(findings, fmt.Sprintf("%s: the DeviceClass %s reads %s with no has() of it before", file, class.name, read))
				}
			}
		}
	}
	return findings, nil
}

// yamlBlocks answers the text of each fenced yaml block of a page.
func yamlBlocks(page string) []string {
	var blocks []string
	var block *strings.Builder
	for _, line := range strings.Split(page, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case block == nil && (trimmed == "```yaml" || trimmed == "```yml"):
			block = &strings.Builder{}
		case block != nil && trimmed == "```":
			blocks = append(blocks, block.String())
			block = nil
		case block != nil:
			block.WriteString(line + "\n")
		}
	}
	return blocks
}

type classSelector struct{ name, expression string }

// classSelectors answers the selectors of each DeviceClass in a YAML
// stream. A block of a page that is not YAML, such as a template with
// a placeholder, holds no class.
func classSelectors(text string) []classSelector {
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(text)))
	var found []classSelector
	for {
		var class struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				Selectors []struct {
					CEL struct {
						Expression string `yaml:"expression"`
					} `yaml:"cel"`
				} `yaml:"selectors"`
			} `yaml:"spec"`
		}
		// The stream ends at io.EOF, or at the first document that
		// does not parse.
		if err := decoder.Decode(&class); err != nil {
			return found
		}
		if class.Kind != "DeviceClass" {
			continue
		}
		for _, s := range class.Spec.Selectors {
			found = append(found, classSelector{class.Metadata.Name, s.CEL.Expression})
		}
	}
}
