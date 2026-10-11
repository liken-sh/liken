package capture

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func vector(name, timestamp, member string) string {
	return `<defSwitchVector device="Bare Telescope" name="` + name + `" timestamp="` + timestamp + `"><defSwitch name="` + member + `">On</defSwitch></defSwitchVector>`
}

func TestDefinitions(t *testing.T) {
	replies := []Reply{
		{"baseline", []byte(vector("CONNECTION", "1", "DISCONNECT") + "\n" + vector("ACTIVE_DEVICES", "1", "A") + "\n")},
		{"connect", []byte(vector("TELESCOPE_PIER_SIDE", "2", "PIER_EAST") +
			`<delProperty device="Bare Telescope" name="TELESCOPE_PIER_SIDE"/>` +
			`<setSwitchVector device="Bare Telescope" name="CONNECTION"><oneSwitch name="CONNECT">On</oneSwitch></setSwitchVector>` +
			vector("ACTIVE_DEVICES", "2", "B") +
			`<defNumberVector device="" name=""><defNumber name="">0</defNumber></defNumberVector>`)},
	}

	defs, err := Definitions(replies)
	if err != nil {
		t.Fatal(err)
	}

	want := []Definition{
		// A set changes no definition.
		{"CONNECTION", "baseline", []byte(`<defSwitchVector device="Bare Telescope" name="CONNECTION"><defSwitch name="DISCONNECT">On</defSwitch></defSwitchVector>`)},
		// The last definition wins, in the phase of the first.
		{"ACTIVE_DEVICES", "baseline", []byte(`<defSwitchVector device="Bare Telescope" name="ACTIVE_DEVICES"><defSwitch name="B">On</defSwitch></defSwitchVector>`)},
		// A deleted property stays, and one with no name is left out.
		{"TELESCOPE_PIER_SIDE", "connect", []byte(`<defSwitchVector device="Bare Telescope" name="TELESCOPE_PIER_SIDE"><defSwitch name="PIER_EAST">On</defSwitch></defSwitchVector>`)},
	}
	if !slices.EqualFunc(defs, want, func(a, b Definition) bool {
		return a.Name == b.Name && a.Phase == b.Phase && bytes.Equal(a.XML, b.XML)
	}) {
		t.Errorf("definitions =\n%q\nwant\n%q", defs, want)
	}
}

func TestDefinitionsRefuseAReplyThatEndsInsideATag(t *testing.T) {
	if _, err := Definitions([]Reply{{"connect", []byte(`<defSwitchVector name="A`)}}); err == nil {
		t.Error("Definitions read a reply that ends inside a tag")
	}
}

const driverInfo = `<defTextVector device="Bare CCD" name="DRIVER_INFO">
    <defText name="DRIVER_EXEC">
indi_bare_ccd
    </defText>
    <defText name="DRIVER_INTERFACE">
22
    </defText>
</defTextVector>`

func TestWriteGroupsByPhaseAndLeavesOutTheExcluded(t *testing.T) {
	defs := []Definition{
		{"DRIVER_INFO", "baseline", []byte(driverInfo)},
		{"CCD_INFO", "baseline", []byte(`<defNumberVector name="CCD_INFO"/>`)},
		{"CCD_EXPOSURE", "connect", []byte("\n<defNumberVector name=\"CCD_EXPOSURE\"/>\n")},
	}
	header := Header{Driver: "indi_bare_ccd", Device: `Bare "CCD"`, INDI: "2.2.5", Interface: Interface(defs)}
	var out bytes.Buffer

	if err := Write(&out, header, defs, map[string]bool{"DRIVER_INFO": true}); err != nil {
		t.Fatal(err)
	}

	want := `<capture driver="indi_bare_ccd" device="Bare &#34;CCD&#34;" indi="2.2.5" interface="22">
<phase name="baseline">
<defNumberVector name="CCD_INFO"/>
</phase>
<phase name="connect">
<defNumberVector name="CCD_EXPOSURE"/>
</phase>
</capture>
`
	if out.String() != want {
		t.Errorf("capture =\n%s\nwant\n%s", out.String(), want)
	}
	c, err := Read(&out)
	if err != nil {
		t.Fatal(err)
	}
	if c.Header != header || !c.Defines("CCD_EXPOSURE") || c.Defines("DRIVER_INFO") {
		t.Errorf("Read gave %+v", c)
	}
}

func TestInterfaceIsEmptyWithNoDriverInfo(t *testing.T) {
	cases := []struct {
		name string
		defs []Definition
	}{
		{"no DRIVER_INFO", []Definition{{"CCD_INFO", "baseline", []byte(`<defNumberVector name="CCD_INFO"/>`)}}},
		{"no DRIVER_INTERFACE", []Definition{{"DRIVER_INFO", "baseline", []byte(`<defTextVector name="DRIVER_INFO"/>`)}}},
		{"no XML", []Definition{{"DRIVER_INFO", "baseline", []byte(`<defTextVector`)}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Interface(c.defs); got != "" {
				t.Errorf("Interface = %q, want none", got)
			}
		})
	}
}

func TestWriteWithNoDefinitions(t *testing.T) {
	var out bytes.Buffer
	if err := Write(&out, Header{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), "\">\n</capture>\n") {
		t.Errorf("capture = %q, want a root with no phase", out.String())
	}
}
