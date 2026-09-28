package hardware

import (
	"testing"

	"github.com/liken-sh/liken/machine"
)

// pulse8Interfaces is a Pulse-Eight adapter after cdc_acm bound it: the
// communications interface that owns the tty, the data interface that
// cdc_acm claims beside it, and the HID interface that hid-generic
// binds for the firmware's keyboard mode.
func pulse8Interfaces(serial string) []Device {
	return []Device{
		{Bus: "usb", Address: "1-4:1.0", Modalias: "usb:v2548p1002d0200dc02dsc00dp00ic02isc02ip01in00",
			Driver: "cdc_acm", Name: "Pulse-Eight CEC Adapter", Class: "communications", ClassCode: "02",
			Vendor: "2548", Product: "1002", Serial: serial},
		{Bus: "usb", Address: "1-4:1.1", Modalias: "usb:v2548p1002d0200dc02dsc00dp00ic0Aisc00ip00in01",
			Driver: "cdc_acm", Name: "Pulse-Eight CEC Adapter", Class: "cdc-data", ClassCode: "0a",
			Vendor: "2548", Product: "1002", Serial: serial},
		{Bus: "usb", Address: "1-4:1.2", Modalias: "usb:v2548p1002d0200dc02dsc00dp00ic03isc00ip00in02",
			Driver: "usbhid", Name: "Pulse-Eight CEC Adapter", Class: "hid", ClassCode: "03",
			Vendor: "2548", Product: "1002", Serial: serial},
	}
}

// After cdc_acm binds, the adapter has a driver and still does not
// work, so the report keeps its serial line on the list and names the
// rest of the fix.
func TestUnclaimedKeepsAKnownAdapterNoEntryAttaches(t *testing.T) {
	c := catalog(t, stickAliases, nil, nil)

	got := c.Unclaimed(pulse8Interfaces(""), nil)

	if len(got) != 1 {
		t.Fatalf("Unclaimed = %+v, want the communications interface alone", got)
	}
	u := got[0]
	if u.Modalias != "usb:v2548p1002d0200dc02dsc00dp00ic02isc02ip01in00" || u.Bus != "usb" ||
		u.Name != "Pulse-Eight CEC Adapter" || u.Class != "communications" || len(u.Candidates) != 0 {
		t.Errorf("entry = %+v", u)
	}
	if u.Message != "for the kernel CEC driver, declare serport and pulse8_cec in spec.modules and a pulse8-cec entry in spec.serio; the entry withholds the tty from workloads, so leave it out when a pod drives the adapter over its serial line" {
		t.Errorf("Message = %q", u.Message)
	}
}

func TestUnclaimedDropsAnAdapterAnEntryAttaches(t *testing.T) {
	c := catalog(t, stickAliases, nil, nil)
	tests := []struct {
		name   string
		serial string
		entry  machine.SerioAttachment
		listed bool
	}{
		{"an entry for the model", "A1",
			machine.SerioAttachment{Protocol: "pulse8-cec", USB: machine.SerioUSB{Vendor: "2548", Product: "1002"}}, false},
		{"an entry for this unit", "A1",
			machine.SerioAttachment{Protocol: "pulse8-cec", USB: machine.SerioUSB{Vendor: "2548", Product: "1002", Serial: "A1"}}, false},
		{"an entry for another unit", "A2",
			machine.SerioAttachment{Protocol: "pulse8-cec", USB: machine.SerioUSB{Vendor: "2548", Product: "1002", Serial: "A1"}}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := c.Unclaimed(pulse8Interfaces(test.serial), []machine.SerioAttachment{test.entry})
			if listed := len(got) == 1; listed != test.listed {
				t.Errorf("Unclaimed = %+v", got)
			}
		})
	}
}

// Before cdc_acm binds, the ordinary report names cdc_acm, and the
// adapter's line is not yet a line, so the serio message waits.
func TestUnclaimedNamesTheLineDriverFirst(t *testing.T) {
	c := catalog(t, "alias usb:v*p*d*dc*dsc*dp*ic02isc02ip01in* cdc_acm\n", []string{"cdc_acm"}, nil)
	devices := pulse8Interfaces("")
	devices[0].Driver = ""

	got := c.Unclaimed(devices, nil)

	if len(got) != 1 || got[0].Message != "declare cdc_acm in spec.modules" {
		t.Errorf("Unclaimed = %+v", got)
	}
}

func TestUnclaimedIgnoresAnAdapterTheTableDoesNotList(t *testing.T) {
	c := catalog(t, stickAliases, nil, nil)
	devices := pulse8Interfaces("")
	devices[0].Product = "1001"

	if got := c.Unclaimed(devices, nil); got != nil {
		t.Errorf("Unclaimed = %+v", got)
	}
}
