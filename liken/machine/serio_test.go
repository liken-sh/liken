package machine

import (
	"slices"
	"strings"
	"testing"
)

// pulse8 is the one attachment the testbed declares: the Pulse-Eight
// USB-CEC adapter, matched by its USB identity alone.
var pulse8 = SerioAttachment{
	Protocol: "pulse8-cec",
	USB:      SerioUSB{Vendor: "2548", Product: "1002"},
}

// The table carries the values inputattach.c uses and the kernel's
// serio.h defines. A wrong type number binds no driver at all, so the
// numbers are checked against their sources here.
func TestTheProtocolTableCarriesTheKernelsValues(t *testing.T) {
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
				t.Fatalf("%s is not in the table", test.name)
			}
			if p.Type != test.serio || p.Driver != test.driver || p.Baud != 9600 ||
				p.LineDriver != "cdc_acm" || p.Discipline != "serport" {
				t.Errorf("got %+v", p)
			}
		})
	}
}

func TestAnUnknownProtocolIsNotInTheTable(t *testing.T) {
	if _, ok := LookupSerioProtocol("pulse9-cec"); ok {
		t.Error("the table must hold only the examined protocols")
	}
}

// The unclaimed report names a known adapter by its identity, so the
// table must say which protocol an identity speaks.
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
				t.Errorf("got %q", got)
			}
		})
	}
}

func TestSerioProtocolNamesAreSorted(t *testing.T) {
	names := SerioProtocolNames()
	if !slices.IsSorted(names) || len(names) != 2 {
		t.Errorf("got %v", names)
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
				t.Errorf("got %v", got)
			}
		})
	}
}

func TestAnAttachmentNamesItselfForAPerson(t *testing.T) {
	withSerial := pulse8
	withSerial.USB.Serial = "A1"
	if got := pulse8.String(); got != "pulse8-cec 2548:1002" {
		t.Errorf("got %q", got)
	}
	if got := withSerial.String(); got != "pulse8-cec 2548:1002 serial A1" {
		t.Errorf("got %q", got)
	}
}

func TestValidateSerioAcceptsTheDeclaredShapes(t *testing.T) {
	withSerial := pulse8
	withSerial.USB.Serial = "0000080D2A5D"
	if err := ValidateSerio([]SerioAttachment{pulse8, withSerial}); err != nil {
		t.Error(err)
	}
	if err := ValidateSerio(nil); err != nil {
		t.Error(err)
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
				t.Errorf("got %v", err)
			}
		})
	}
}

// Two entries that match the same serial line would start two holders
// on one tty, so the second is refused.
func TestValidateSerioRefusesADuplicate(t *testing.T) {
	err := ValidateSerio([]SerioAttachment{pulse8, pulse8})
	if err == nil || !strings.Contains(err.Error(), "entries 0 and 1") {
		t.Errorf("got %v", err)
	}
}

// The set difference keys an entry on all four fields, so a changed
// serial is one retraction and one addition, the same as a changed
// module name.
func TestSerioSetDiffComparesWholeEntries(t *testing.T) {
	rainshadow := SerioAttachment{Protocol: "rainshadow-cec", USB: SerioUSB{Vendor: "0000", Product: "0001"}}
	withSerial := pulse8
	withSerial.USB.Serial = "A1"
	tests := []struct {
		name              string
		desired, actuated []SerioAttachment
		added, retracted  []SerioAttachment
	}{
		{"no change", []SerioAttachment{pulse8}, []SerioAttachment{pulse8}, nil, nil},
		{"an addition", []SerioAttachment{pulse8, rainshadow}, []SerioAttachment{pulse8}, []SerioAttachment{rainshadow}, nil},
		{"a retraction", nil, []SerioAttachment{pulse8}, nil, []SerioAttachment{pulse8}},
		{"a changed serial", []SerioAttachment{withSerial}, []SerioAttachment{pulse8}, []SerioAttachment{withSerial}, []SerioAttachment{pulse8}},
		{"a reorder", []SerioAttachment{rainshadow, pulse8}, []SerioAttachment{pulse8, rainshadow}, nil, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			added, retracted := SerioSetDiff(test.desired, test.actuated)
			if !slices.Equal(added, test.added) || !slices.Equal(retracted, test.retracted) {
				t.Errorf("got added %v, retracted %v", added, retracted)
			}
		})
	}
}

// One line per entry is what the live-load rule counts, the way
// ModulesDrift writes one line per added module.
func TestSerioDriftWritesOneLinePerEntry(t *testing.T) {
	rainshadow := SerioAttachment{Protocol: "rainshadow-cec", USB: SerioUSB{Vendor: "0000", Product: "0001"}}
	got := SerioDrift([]SerioAttachment{rainshadow}, []SerioAttachment{pulse8})
	want := []string{
		"serio: rainshadow-cec 0000:0001 declared but this boot ran without it",
		"serio: pulse8-cec 2548:1002 no longer declared but this boot ran with it",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q", got)
	}
}
