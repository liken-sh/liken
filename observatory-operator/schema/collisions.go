package schema

import (
	"fmt"

	"sigs.k8s.io/yaml"
)

// handWritten returns the fields that a kind's CRD template defines by
// hand, such as the parent, `driver`, `pod`, and the procedures, keyed
// by spec and status.
func handWritten(dir, plural string) (map[string]map[string]any, error) {
	rendered, err := renderCRD(dir, plural, "", "")
	if err != nil {
		return nil, err
	}
	var crd struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPIV3Schema struct {
						Properties map[string]struct {
							Properties map[string]any `json:"properties"`
						} `json:"properties"`
					} `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &crd); err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	for _, v := range crd.Spec.Versions {
		for half, schema := range v.Schema.OpenAPIV3Schema.Properties {
			out[half] = schema.Properties
		}
	}
	return out, nil
}

// collisions returns an error when a typed field has the same name as a
// hand-written field of the template, or when two typed fields share a
// name. The Go types embed the typed fields beside the hand-written
// ones, and when two embedded structs have a field of the same name,
// encoding/json drops both without an error.
func collisions(kind string, hand map[string]map[string]any, typed map[string][]field) error {
	for _, half := range []string{"spec", "status"} {
		seen := map[string]bool{}
		for _, f := range typed[half] {
			if _, taken := hand[half][f.JSON]; taken {
				return fmt.Errorf("%s: the typed field %s.%s, from %s, has the same name as a field that the template writes", kind, half, f.JSON, f.INDI)
			}
			if seen[f.JSON] {
				return fmt.Errorf("%s has two typed fields named %s.%s", kind, half, f.JSON)
			}
			seen[f.JSON] = true
		}
	}
	return nil
}
