package machine

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"sigs.k8s.io/yaml"
)

// crdSerioField is the part of the spec.serio schema that the Go code
// must match: the protocol enum, the USB patterns, and maxItems.
type crdSerioField struct {
	MaxItems int `json:"maxItems"`
	Items    struct {
		Properties struct {
			Protocol struct {
				Enum []string `json:"enum"`
			} `json:"protocol"`
			USB struct {
				Properties struct {
					Vendor  struct{ Pattern string } `json:"vendor"`
					Product struct{ Pattern string } `json:"product"`
					Serial  struct{ Pattern string } `json:"serial"`
				} `json:"properties"`
			} `json:"usb"`
		} `json:"properties"`
	} `json:"items"`
}

func readSerioCRD(t *testing.T) crdSerioField {
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
									Serio crdSerioField `json:"serio"`
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
	return crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec.Properties.Serio
}

// A protocol that the API server accepts and init does not support would
// reach the machine as an entry that never attaches, so the enum and
// the protocol table must list the same names.
func TestTheCRDEnumIsTheProtocolTable(t *testing.T) {
	enum := slices.Sorted(slices.Values(readSerioCRD(t).Items.Properties.Protocol.Enum))
	if !slices.Equal(enum, SerioProtocolNames()) {
		t.Errorf("the CRD enum is %v, want the protocol table %v", enum, SerioProtocolNames())
	}
}

// ValidateSerio checks a manifest that the API server never saw, so it
// must accept and refuse the same values as the CRD patterns.
func TestTheCRDPatternsMatchValidateSerio(t *testing.T) {
	usb := readSerioCRD(t).Items.Properties.USB.Properties
	tests := []struct {
		name      string
		crd       string
		goPattern *regexp.Regexp
		value     string
	}{
		{"a vendor", usb.Vendor.Pattern, usbIDPattern, "2548"},
		{"an uppercase vendor", usb.Vendor.Pattern, usbIDPattern, "25A8"},
		{"a product", usb.Product.Pattern, usbIDPattern, "1002"},
		{"a long product", usb.Product.Pattern, usbIDPattern, "10020"},
		{"a serial", usb.Serial.Pattern, serialPattern, "0000080D2A5D"},
		{"a serial with a space", usb.Serial.Pattern, serialPattern, "A 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			crd := regexp.MustCompile(test.crd).MatchString(test.value)
			if crd != test.goPattern.MatchString(test.value) {
				t.Errorf("the CRD pattern gives %v for %q, and the Go pattern gives %v", crd, test.value, test.goPattern.MatchString(test.value))
			}
		})
	}
}

// Each entry runs one goroutine with one open tty, and a machine has
// few adapters, so the list is limited to 8 entries.
func TestTheCRDBoundsTheSerioList(t *testing.T) {
	if got := readSerioCRD(t).MaxItems; got != 8 {
		t.Errorf("the CRD maxItems is %d, want 8", got)
	}
}
