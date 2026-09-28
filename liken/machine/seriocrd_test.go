package machine

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"sigs.k8s.io/yaml"
)

// crdSerioField is the part of the spec.serio schema that the Go side
// must agree with: the protocol enum and the three patterns.
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

// A protocol the API server admits and init does not know would reach
// the machine as an entry that never attaches, so the enum and the
// table are one list.
func TestTheCRDEnumIsTheProtocolTable(t *testing.T) {
	enum := slices.Sorted(slices.Values(readSerioCRD(t).Items.Properties.Protocol.Enum))
	if !slices.Equal(enum, SerioProtocolNames()) {
		t.Errorf("the CRD admits %v, the table holds %v", enum, SerioProtocolNames())
	}
}

// ValidateSerio applies the same patterns to a manifest that no API
// server admitted, so the two must accept and refuse the same values.
func TestTheCRDPatternsMatchValidateSerio(t *testing.T) {
	usb := readSerioCRD(t).Items.Properties.USB.Properties
	tests := []struct {
		name  string
		crd   string
		goes  *regexp.Regexp
		value string
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
			if crd != test.goes.MatchString(test.value) {
				t.Errorf("the CRD says %v for %q, and ValidateSerio disagrees", crd, test.value)
			}
		})
	}
}

// A holder is one goroutine and one open tty, and a machine carries a
// handful of adapters at most, so the list has a small bound.
func TestTheCRDBoundsTheSerioList(t *testing.T) {
	if got := readSerioCRD(t).MaxItems; got != 8 {
		t.Errorf("got %d", got)
	}
}
