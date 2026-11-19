package machine

import (
	"os"
	"testing"

	"sigs.k8s.io/yaml"
)

func readSysctls(t *testing.T) crdMapField {
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
								Properties struct {
									Sysctls crdMapField `json:"sysctls"`
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
	return crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec.Properties.Sysctls
}

// A sysctl name is dotted, as the kernel writes it, or slashed, for a
// segment that has a dot, such as a VLAN interface. The CRD refuses the
// names that sysctlPath refuses because they would not map to a file
// under /proc/sys: an absolute name, one with "." or ".." as a segment,
// and one with an empty segment.
func TestMachineCRDRefusesASysctlNameOutsideProcSys(t *testing.T) {
	pattern := keyRulePattern(t, readSysctls(t).Validations)
	cases := []struct {
		key      string
		accepted bool
	}{
		{"vm.max_map_count", true},
		{"net.ipv4.ip_forward", true},
		{"net.ipv4.conf.all.forwarding", true},
		{"net.ipv4.conf.eth-1.rp_filter", true},
		{"net/ipv4/conf/eth0.100/forwarding", true},
		{"kernel.core_pattern", true},
		{"../etc/passwd", false},
		{"/etc/passwd", false},
		{"net/../../etc/passwd", false},
		{"net..ipv4", false},
		{".vm.max_map_count", false},
		{"vm.max_map_count.", false},
		{"net//ipv4", false},
		{"net/ipv4/", false},
		{"net/./ipv4", false},
		{"vm max_map_count", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			if got := pattern.MatchString(tc.key); got != tc.accepted {
				t.Errorf("the key rule accepts %q is %v, want %v", tc.key, got, tc.accepted)
			}
		})
	}
}

// The API server refuses a schema with a CEL rule over every key
// unless it can bound the rule's cost, which needs maxProperties.
func TestMachineCRDBoundsTheSysctlsItChecks(t *testing.T) {
	if got := readSysctls(t).MaxProperties; got == 0 {
		t.Error("spec.sysctls has no maxProperties, want one so the API server can bound the key rule's cost")
	}
}
