package machine

import (
	"os"
	"testing"

	"sigs.k8s.io/yaml"
)

// readSysctls reads the schema of spec.sysctls from the Machine CRD.
func readSysctls(t *testing.T) crdMapField {
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
		t.Fatal(err)
	}
	return crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec.Properties.Sysctls
}

// A sysctl name names a file under /proc/sys, in the kernel's dotted
// form or in the slashed form that a name with a dot in one segment
// needs. Admission refuses a name that cannot name a file there: one
// that is absolute, climbs out with "..", or has an empty segment, the
// names sysctlPath refuses before it reaches the kernel.
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
				t.Errorf("%q accepted = %v, want %v", tc.key, got, tc.accepted)
			}
		})
	}
}

// The CEL cost of a rule over every key needs a bound on the number of
// keys, and the API server refuses a schema whose rule it cannot bound.
func TestMachineCRDBoundsTheSysctlsItChecks(t *testing.T) {
	if got := readSysctls(t).MaxProperties; got == 0 {
		t.Error("spec.sysctls must set maxProperties, so the key rule has a bounded cost")
	}
}
