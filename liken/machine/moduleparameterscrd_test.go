package machine

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// crdValidation is one CEL rule and the message the API server returns
// when the rule refuses a document.
type crdValidation struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// crdMapField is the schema of a map-valued field: the cap on its
// size, the shape of its values, and the rules on its keys.
type crdMapField struct {
	Type                 string          `json:"type"`
	MaxProperties        int             `json:"maxProperties"`
	Validations          []crdValidation `json:"x-kubernetes-validations"`
	AdditionalProperties struct {
		Type      string `json:"type"`
		Pattern   string `json:"pattern"`
		MaxLength int    `json:"maxLength"`
	} `json:"additionalProperties"`
}

// readMachineCRDSpec reads the spec node of the Machine CRD: the
// moduleParameters field and the rules that read the whole spec.
func readMachineCRDSpec(t *testing.T) (crdMapField, []crdValidation, int) {
	t.Helper()
	raw, err := os.ReadFile("manifests/machines-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPIV3Schema struct {
						Properties struct {
							Spec struct {
								Validations []crdValidation `json:"x-kubernetes-validations"`
								Properties  struct {
									ModuleParameters crdMapField `json:"moduleParameters"`
									Modules          struct {
										MaxItems int `json:"maxItems"`
									} `json:"modules"`
								} `json:"properties"`
							} `json:"spec"`
						} `json:"properties"`
					} `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	spec := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec
	return spec.Properties.ModuleParameters, spec.Validations, spec.Properties.Modules.MaxItems
}

func TestMachineCRDCapsModuleParametersLikeTheModuleList(t *testing.T) {
	parameters, _, maxModules := readMachineCRDSpec(t)
	if parameters.Type != "object" || parameters.MaxProperties != maxModules {
		t.Errorf("got %d parameters against %d modules", parameters.MaxProperties, maxModules)
	}
}

// The key pattern is one module name, one dot, and one parameter
// name, which is what the kernel command line and /sys both use.
func TestMachineCRDChecksTheKeyShape(t *testing.T) {
	parameters, _, _ := readMachineCRDSpec(t)
	pattern := keyRulePattern(t, parameters.Validations)
	accepted := []string{"snd_hda_intel.power_save", "i915.enable_guc", "r8169.debug"}
	for _, key := range accepted {
		if !pattern.MatchString(key) {
			t.Errorf("%q is a kernel module parameter name", key)
		}
	}
	refused := []string{"power_save", "snd_hda_intel.", ".power_save", "a.b.c", "snd hda.x"}
	for _, key := range refused {
		if pattern.MatchString(key) {
			t.Errorf("%q is not a module parameter name", key)
		}
	}
}

// keyRulePattern pulls the regular expression out of the CEL rule that
// checks the map's keys, so the test runs the same pattern the API
// server runs.
func keyRulePattern(t *testing.T, validations []crdValidation) *regexp.Regexp {
	t.Helper()
	for _, v := range validations {
		if !strings.Contains(v.Rule, "matches(") {
			continue
		}
		_, rest, _ := strings.Cut(v.Rule, "matches('")
		expression, _, _ := strings.Cut(rest, "')")
		return regexp.MustCompile(expression)
	}
	t.Fatal("moduleParameters must carry a key rule")
	return nil
}

// The parameter string is space-separated, and the kernel's own parser
// treats a double quote as a grouping character, so a value carries
// neither.
func TestMachineCRDRefusesASpaceOrAQuoteInAValue(t *testing.T) {
	parameters, _, _ := readMachineCRDSpec(t)
	pattern := regexp.MustCompile(parameters.AdditionalProperties.Pattern)
	for _, value := range []string{"0", "1,2,3", "0x1e"} {
		if !pattern.MatchString(value) {
			t.Errorf("%q is a value the kernel parses", value)
		}
	}
	for _, value := range []string{"a b", `"a"`, "a\tb"} {
		if pattern.MatchString(value) {
			t.Errorf("%q would break the parameter string", value)
		}
	}
}

// An empty value writes no file in the boot record, and the facts
// tree reads a key with no file as a key that was never declared. The
// machine would then drift against its own spec on every pass, and
// one reboot would not settle it.
func TestMachineCRDRefusesAnEmptyValue(t *testing.T) {
	parameters, _, _ := readMachineCRDSpec(t)
	if regexp.MustCompile(parameters.AdditionalProperties.Pattern).MatchString("") {
		t.Error("a parameter with no value is not a declaration")
	}
}

// A key becomes a file name under boot/moduleParameters, and a name
// past the kernel's own limit fails that write with ENAMETOOLONG
// partway through the map.
func TestMachineCRDBoundsTheKeyLength(t *testing.T) {
	parameters, _, _ := readMachineCRDSpec(t)
	var found bool
	for _, v := range parameters.Validations {
		if !strings.Contains(v.Rule, "size(k)") {
			continue
		}
		found = true
		if !strings.Contains(v.Rule, "129") || !strings.Contains(v.Message, "129") {
			t.Errorf("the rule and its message must name the bound: %q, %q", v.Rule, v.Message)
		}
	}
	if !found {
		t.Error("moduleParameters must bound the length of a key")
	}
}

// A key names a module, and only the module list says which modules
// this machine loads, so the rule that ties them lives on the spec.
func TestMachineCRDTiesAKeyToTheModuleList(t *testing.T) {
	_, validations, _ := readMachineCRDSpec(t)
	var found bool
	for _, v := range validations {
		if strings.Contains(v.Rule, "self.moduleParameters.all") &&
			strings.Contains(v.Rule, "self.modules.exists") {
			found = true
			if !strings.Contains(v.Message, "modules") {
				t.Errorf("the message must name the module list: %q", v.Message)
			}
		}
	}
	if !found {
		t.Error("the spec must refuse a key naming a module that modules does not declare")
	}
}
