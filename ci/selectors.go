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

// The scheduler evaluates a DeviceClass's CEL selectors on every device
// of every driver in the cluster. When a device lacks an attribute or a
// capacity that a selector reads, evaluation fails with "no such key",
// and the scheduler fails every claim of the class on that node, not
// only the claim that would use that device. Devices of one driver can
// differ: a misc device such as /dev/vhost-net has no bus and no bound
// driver. So a class must guard each read with a has() call earlier in
// the selector (CEL evaluates && left to right) or with the optional
// form .? and orValue(). checkSelectors reports each read that has
// neither.
//
// The check covers only DeviceClasses. The scheduler evaluates a
// claim's own selectors only on devices that its class selected, so a
// claim can safely read the attributes that its class guards.

// attributeRead matches one read of a device attribute or capacity by
// its qualified name. Group 1 is the has( in front of it, if any, and
// group 2 is the name.
var attributeRead = regexp.MustCompile(`(has\(\s*)?(device\.(?:attributes|capacity)\["[^"]+"\]\.[A-Za-z_][A-Za-z0-9_]*)`)

// unguardedReads returns each read in the expression that no earlier
// has() of the same name guards, in the order they appear.
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

// checkSelectors returns a finding for each unguarded read in each
// DeviceClass of the repository. It checks the YAML files and the YAML
// blocks of the pages and skills, because readers copy classes from
// them. sourceFiles leaves out plans, which record what was written at
// the time, and test data.
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
		// sourceFiles lists a tracked file that was deleted from the
		// working tree.
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

// yamlBlocks returns the text of each fenced yaml block of a page.
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

// classSelectors returns the selectors of each DeviceClass in a YAML
// stream. It stops at the end of the stream or at the first document
// that does not parse, such as a template with a placeholder, and
// returns the classes before it.
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
