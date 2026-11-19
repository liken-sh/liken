package machine

import (
	"slices"
	"strings"
	"testing"
)

// pulse8 is a Pulse-Eight USB-CEC adapter, matched by its USB identity
// alone.
var pulse8 = SerioAttachment{
	Protocol: "pulse8-cec",
	USB:      SerioUSB{Vendor: "2548", Product: "1002"},
}

// The expected values are copied from inputattach.c and the kernel's
// serio.h, because a wrong type number binds no driver.
func TestProtocolTableMatchesKernelValues(t *testing.T) {
	tests := []struct {
		name   string
		serio  uint8
		driver string
	}{
		{"pulse8-cec", 0x40, "pulse8_cec"},
		{"rainshadow-cec", 0x41, "rainshadow_cec"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p, ok := LookupSerioProtocol(test.name)
			if !ok {
				t.Fatalf("LookupSerioProtocol(%q) found no protocol", test.name)
			}
			if p.Type != test.serio || p.Driver != test.driver || p.Baud != 9600 ||
				p.LineDriver != "cdc_acm" || p.Discipline != "serport" {
				t.Errorf("LookupSerioProtocol(%q) = %+v, want type %v, driver %s, 9600 baud, line driver cdc_acm, discipline serport", test.name, p, test.serio, test.driver)
			}
		})
	}
}

func TestAnUnknownProtocolIsNotInTheTable(t *testing.T) {
	if _, ok := LookupSerioProtocol("pulse9-cec"); ok {
		t.Error("LookupSerioProtocol found pulse9-cec, want only the protocols that were tested against an adapter")
	}
}

// status.hardware.unclaimed uses the table to name the protocol of a
// known adapter.
func TestTheTableNamesTheProtocolOfAKnownAdapter(t *testing.T) {
	tests := []struct {
		name, vendor, product, want string
	}{
		{"the Pulse-Eight", "2548", "1002", "pulse8-cec"},
		{"an unknown adapter", "2548", "1001", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := KnownSerioAdapter(test.vendor, test.product); got != test.want {
				t.Errorf("KnownSerioAdapter(%q, %q) = %q, want %q", test.vendor, test.product, got, test.want)
			}
		})
	}
}

func TestSerioProtocolNamesAreSorted(t *testing.T) {
	names := SerioProtocolNames()
	if !slices.IsSorted(names) || len(names) != 2 {
		t.Errorf("SerioProtocolNames() = %v, want two names in sorted order", names)
	}
}

func TestAnAttachmentMatchesItsUSBIdentity(t *testing.T) {
	withSerial := pulse8
	withSerial.USB.Serial = "A1"
	tests := []struct {
		name                    string
		attachment              SerioAttachment
		vendor, product, serial string
		want                    bool
	}{
		{"vendor and product", pulse8, "2548", "1002", "", true},
		{"any serial without one declared", pulse8, "2548", "1002", "Z9", true},
		{"another product", pulse8, "2548", "1001", "", false},
		{"another vendor", pulse8, "2549", "1002", "", false},
		{"the declared serial", withSerial, "2548", "1002", "A1", true},
		{"another serial", withSerial, "2548", "1002", "A2", false},
		{"no serial when one is declared", withSerial, "2548", "1002", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.attachment.Matches(test.vendor, test.product, test.serial); got != test.want {
				t.Errorf("Matches(%q, %q, %q) = %v, want %v", test.vendor, test.product, test.serial, got, test.want)
			}
		})
	}
}

func TestAnAttachmentNamesItselfForAPerson(t *testing.T) {
	withSerial := pulse8
	withSerial.USB.Serial = "A1"
	if got := pulse8.String(); got != "pulse8-cec 2548:1002" {
		t.Errorf("String() = %q, want %q", got, "pulse8-cec 2548:1002")
	}
	if got := withSerial.String(); got != "pulse8-cec 2548:1002 serial A1" {
		t.Errorf("String() = %q, want %q", got, "pulse8-cec 2548:1002 serial A1")
	}
}

func TestValidateSerioAcceptsTheDeclaredShapes(t *testing.T) {
	withSerial := pulse8
	withSerial.USB.Serial = "0000080D2A5D"
	if err := ValidateSerio([]SerioAttachment{pulse8, withSerial}); err != nil {
		t.Errorf("ValidateSerio refused a declared shape: %v", err)
	}
	if err := ValidateSerio(nil); err != nil {
		t.Errorf("ValidateSerio refused a declared shape: %v", err)
	}
}

func TestValidateSerioRefusesAnEntryThatCannotMatch(t *testing.T) {
	tests := []struct {
		name       string
		attachment SerioAttachment
		want       string
	}{
		{"an unknown protocol", SerioAttachment{Protocol: "pulse9-cec", USB: pulse8.USB}, "pulse9-cec"},
		{"uppercase hex", SerioAttachment{Protocol: "pulse8-cec", USB: SerioUSB{Vendor: "25A8", Product: "1002"}}, "vendor"},
		{"a short product", SerioAttachment{Protocol: "pulse8-cec", USB: SerioUSB{Vendor: "2548", Product: "102"}}, "product"},
		{"a prefixed vendor", SerioAttachment{Protocol: "pulse8-cec", USB: SerioUSB{Vendor: "0x2548", Product: "1002"}}, "vendor"},
		{"a serial with a space", SerioAttachment{Protocol: "pulse8-cec", USB: SerioUSB{Vendor: "2548", Product: "1002", Serial: "A 1"}}, "serial"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSerio([]SerioAttachment{test.attachment})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("ValidateSerio returned %v, want an error that contains %q", err, test.want)
			}
		})
	}
}

// Two identical entries would attach the same tty twice, so
// ValidateSerio rejects the second.
func TestValidateSerioRefusesADuplicate(t *testing.T) {
	err := ValidateSerio([]SerioAttachment{pulse8, pulse8})
	if err == nil || !strings.Contains(err.Error(), "spec.serio[0] and spec.serio[1]") {
		t.Errorf("ValidateSerio returned %v, want an error that contains %q", err, "spec.serio[0] and spec.serio[1]")
	}
}

// SerioSetDiff compares whole entries, so a changed serial is one
// retraction and one addition.
func TestSerioSetDiffComparesWholeEntries(t *testing.T) {
	rainshadow := SerioAttachment{Protocol: "rainshadow-cec", USB: SerioUSB{Vendor: "0000", Product: "0001"}}
	withSerial := pulse8
	withSerial.USB.Serial = "A1"
	tests := []struct {
		name             string
		desired, booted  []SerioAttachment
		added, retracted []SerioAttachment
	}{
		{"no change", []SerioAttachment{pulse8}, []SerioAttachment{pulse8}, nil, nil},
		{"an addition", []SerioAttachment{pulse8, rainshadow}, []SerioAttachment{pulse8}, []SerioAttachment{rainshadow}, nil},
		{"a retraction", nil, []SerioAttachment{pulse8}, nil, []SerioAttachment{pulse8}},
		{"a changed serial", []SerioAttachment{withSerial}, []SerioAttachment{pulse8}, []SerioAttachment{withSerial}, []SerioAttachment{pulse8}},
		{"a reorder", []SerioAttachment{rainshadow, pulse8}, []SerioAttachment{pulse8, rainshadow}, nil, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			added, retracted := SerioSetDiff(test.desired, test.booted)
			if !slices.Equal(added, test.added) || !slices.Equal(retracted, test.retracted) {
				t.Errorf("SerioSetDiff returned added %v and retracted %v, want added %v and retracted %v", added, retracted, test.added, test.retracted)
			}
		})
	}
}

func TestSerioDriftWritesOneLinePerEntry(t *testing.T) {
	rainshadow := SerioAttachment{Protocol: "rainshadow-cec", USB: SerioUSB{Vendor: "0000", Product: "0001"}}
	got := SerioDrift([]SerioAttachment{rainshadow}, []SerioAttachment{pulse8})
	want := []string{
		"serio: rainshadow-cec 0000:0001 declared but this boot ran without it",
		"serio: pulse8-cec 2548:1002 no longer declared but this boot ran with it",
	}
	if !slices.Equal(got, want) {
		t.Errorf("SerioDrift = %q, want %q", got, want)
	}
}
