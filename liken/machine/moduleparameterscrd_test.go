package machine

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// crdValidation is one CEL rule and the message that the API server
// returns when the rule rejects a document.
type crdValidation struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// crdMapField is the schema of a map field: its maximum size, the
// pattern of its values, and the rules on its keys.
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

// readMachineCRDSpec reads the spec schema of the Machine CRD: the
// moduleParameters field and the rules that validate the whole spec.
func readMachineCRDSpec(t *testing.T) (crdMapField, []crdValidation, int) {
	t.Helper()
	raw, err := os.ReadFile("manifests/machines-crd.yaml")
	if err != nil {
		t.Fatalf("reading the Machine CRD: %v", err)
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
		t.Fatalf("parsing the Machine CRD: %v", err)
	}
	spec := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec
	return spec.Properties.ModuleParameters, spec.Validations, spec.Properties.Modules.MaxItems
}

func TestMachineCRDCapsModuleParametersLikeTheModuleList(t *testing.T) {
	parameters, _, maxModules := readMachineCRDSpec(t)
	if parameters.Type != "object" || parameters.MaxProperties != maxModules {
		t.Errorf("moduleParameters allows %d properties, want the %d that modules allows", parameters.MaxProperties, maxModules)
	}
}

// A key is a module name, a dot, and a parameter name, as on the
// kernel command line.
func TestMachineCRDChecksTheKeyShape(t *testing.T) {
	parameters, _, _ := readMachineCRDSpec(t)
	pattern := keyRulePattern(t, parameters.Validations)
	accepted := []string{"snd_hda_intel.power_save", "i915.enable_guc", "r8169.debug"}
	for _, key := range accepted {
		if !pattern.MatchString(key) {
			t.Errorf("the key rule refuses %q, want it accepted as a kernel module parameter name", key)
		}
	}
	refused := []string{"power_save", "snd_hda_intel.", ".power_save", "a.b.c", "snd hda.x"}
	for _, key := range refused {
		if pattern.MatchString(key) {
			t.Errorf("the key rule accepts %q, want it refused", key)
		}
	}
}

// keyRulePattern returns the regular expression of the first CEL
// rule that calls matches(), so the test checks the pattern that the
// API server runs.
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
	t.Fatal("moduleParameters has no rule that calls matches(), want a key rule")
	return nil
}

// Init passes a module's parameters to finit_module as one string
// separated by spaces, and the kernel's parser groups text in double
// quotes, so a value cannot contain whitespace or a quote.
func TestMachineCRDRefusesASpaceOrAQuoteInAValue(t *testing.T) {
	parameters, _, _ := readMachineCRDSpec(t)
	pattern := regexp.MustCompile(parameters.AdditionalProperties.Pattern)
	for _, value := range []string{"0", "1,2,3", "0x1e"} {
		if !pattern.MatchString(value) {
			t.Errorf("the value pattern refuses %q, want it accepted", value)
		}
	}
	for _, value := range []string{"a b", `"a"`, "a\tb"} {
		if pattern.MatchString(value) {
			t.Errorf("the value pattern accepts %q, want it refused because it would break the parameter string", value)
		}
	}
}

// An empty value writes no file under boot/moduleParameters, so the
// key reads back as undeclared. The machine would then differ from
// its spec on every pass, and a reboot would not fix it.
func TestMachineCRDRefusesAnEmptyValue(t *testing.T) {
	parameters, _, _ := readMachineCRDSpec(t)
	if regexp.MustCompile(parameters.AdditionalProperties.Pattern).MatchString("") {
		t.Error("the value pattern accepts an empty value, want it refused")
	}
}

// Each key is a file name under boot/moduleParameters, and a name
// longer than the filesystem allows fails with ENAMETOOLONG partway
// through writing the map.
func TestMachineCRDBoundsTheKeyLength(t *testing.T) {
	parameters, _, _ := readMachineCRDSpec(t)
	var found bool
	for _, v := range parameters.Validations {
		if !strings.Contains(v.Rule, "size(k)") {
			continue
		}
		found = true
		if !strings.Contains(v.Rule, "129") || !strings.Contains(v.Message, "129") {
			t.Errorf("the rule is %q and its message is %q, want both to name the bound 129", v.Rule, v.Message)
		}
	}
	if !found {
		t.Error("moduleParameters has no rule on size(k), want one that bounds the length of a key")
	}
}

// The rule that checks each key's module against spec.modules must be
// on the spec, because a rule on the map cannot read spec.modules.
func TestMachineCRDTiesAKeyToTheModuleList(t *testing.T) {
	_, validations, _ := readMachineCRDSpec(t)
	var found bool
	for _, v := range validations {
		if strings.Contains(v.Rule, "self.moduleParameters.all") &&
			strings.Contains(v.Rule, "self.modules.exists") {
			found = true
			if !strings.Contains(v.Message, "modules") {
				t.Errorf("the message is %q, want it to name modules", v.Message)
			}
		}
	}
	if !found {
		t.Error("the spec has no rule that refuses a key for a module that modules does not list")
	}
}
