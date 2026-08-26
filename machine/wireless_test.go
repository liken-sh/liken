package machine

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// ssidCase is one SSID and the two answers it must get: valid is
// what Validate answers, and matches is what the CRD pattern
// answers. The two columns differ only where the gap between the two
// copies of the rule is deliberate.
type ssidCase struct {
	name    string
	ssid    string
	valid   bool
	matches bool
}

// ssidCases is the whole SSID rule in one table: the file-naming
// half, which both copies of the rule enforce, and the control-byte
// half, which only Validate enforces.
var ssidCases = []ssidCase{
	{name: "a plain name", ssid: "stonypoint", valid: true, matches: true},
	{name: "spaces and punctuation", ssid: "Stony Point 5G!", valid: true, matches: true},
	{name: "a dot inside", ssid: "home.arpa", valid: true, matches: true},
	{name: "a leading dot", ssid: ".hidden", valid: true, matches: true},
	{name: "two leading dots", ssid: "..hidden", valid: true, matches: true},
	{name: "three dots", ssid: "...", valid: true, matches: true},
	{name: "non-ASCII", ssid: "café", valid: true, matches: true},
	{name: "empty", ssid: "", valid: false, matches: false},
	{name: "the current directory", ssid: ".", valid: false, matches: false},
	{name: "the parent directory", ssid: "..", valid: false, matches: false},
	{name: "a separator inside", ssid: "home/guest", valid: false, matches: false},
	{name: "only a separator", ssid: "/", valid: false, matches: false},
	{name: "a trailing separator", ssid: "home/", valid: false, matches: false},
	{name: "a newline", ssid: "home\nguest", valid: false, matches: true},
	{name: "a tab", ssid: "home\tguest", valid: false, matches: true},
	{name: "an escape", ssid: "home\x1bguest", valid: false, matches: true},
	{name: "a delete", ssid: "home\x7fguest", valid: false, matches: true},
	{name: "a NUL", ssid: "home\x00guest", valid: false, matches: true},
}

// wirelessSpec builds the one-radio spec the wireless checks run
// against.
func wirelessSpec(ssid string, security WirelessSecurity) NetworkSpec {
	return NetworkSpec{Interfaces: []InterfaceSpec{
		{Name: "wlan0", Wireless: &WirelessSpec{SSID: ssid, Security: security}},
	}}
}

func TestNetworkValidateJudgesAnSSIDByWhetherItCanNameAFile(t *testing.T) {
	for _, tc := range ssidCases {
		t.Run(tc.name, func(t *testing.T) {
			err := wirelessSpec(tc.ssid, WirelessWPAPSK).Validate()
			if tc.valid && err != nil {
				t.Errorf("%q must be accepted: %v", tc.ssid, err)
			}
			if !tc.valid && err == nil {
				t.Errorf("%q must be refused", tc.ssid)
			}
		})
	}
}

func TestNetworkValidateNamesTheInterfaceThatHoldsAControlByte(t *testing.T) {
	// The SSID reaches a line-oriented fact file and a generated
	// supplicant configuration, so a newline breaks the first and
	// would end a line early in the second. The error must name the
	// interface and spell the byte, because the raw value prints as
	// nothing a person can read.
	err := wirelessSpec("home\nguest", WirelessWPAPSK).Validate()
	if err == nil {
		t.Fatal("an SSID with a newline must be refused")
	}
	if !strings.Contains(err.Error(), "wlan0") {
		t.Errorf("the error must name the interface: %v", err)
	}
	if !strings.Contains(err.Error(), `\n`) {
		t.Errorf("the error must spell the byte rather than print it: %v", err)
	}
}

func TestNetworkValidateAcceptsAnSSIDOf32Octets(t *testing.T) {
	if err := wirelessSpec(strings.Repeat("s", 32), WirelessWPAPSK).Validate(); err != nil {
		t.Error(err)
	}
}

func TestNetworkValidateRejectsAnSSIDLongerThan32Octets(t *testing.T) {
	// 802.11 carries an SSID in an element of at most 32 octets, so
	// a longer value names a network no radio can join.
	err := wirelessSpec(strings.Repeat("s", 33), WirelessWPAPSK).Validate()
	if err == nil {
		t.Fatal("an SSID of 33 octets must be refused")
	}
	if !strings.Contains(err.Error(), "32") {
		t.Errorf("the error must name the limit: %v", err)
	}
}

func TestNetworkValidateCountsAnSSIDInOctetsNotCharacters(t *testing.T) {
	// The limit counts octets, not characters: sixteen two-octet
	// characters fill the element exactly and seventeen overrun it.
	if err := wirelessSpec(strings.Repeat("é", 16), WirelessWPAPSK).Validate(); err != nil {
		t.Errorf("16 two-octet characters fit in 32 octets: %v", err)
	}
	if err := wirelessSpec(strings.Repeat("é", 17), WirelessWPAPSK).Validate(); err == nil {
		t.Error("17 two-octet characters overrun 32 octets")
	}
}

func TestNetworkValidateAcceptsBothSecurityValues(t *testing.T) {
	for _, security := range []WirelessSecurity{WirelessWPAPSK, WirelessOpen, ""} {
		t.Run(string(security), func(t *testing.T) {
			if err := wirelessSpec("stonypoint", security).Validate(); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestNetworkValidateRejectsAnUnknownSecurityValue(t *testing.T) {
	// The API server refuses a value outside the enum, but a
	// manifest from a stick reaches init without that check, so the
	// Go copy of the rule must refuse it too.
	err := wirelessSpec("stonypoint", "wep").Validate()
	if err == nil {
		t.Fatal("an unknown security value must be refused")
	}
	if !strings.Contains(err.Error(), "wep") {
		t.Errorf("the error must name the value: %v", err)
	}
}

func TestNetworkValidateLeavesAWiredInterfaceAlone(t *testing.T) {
	spec := NetworkSpec{Interfaces: []InterfaceSpec{{Name: "eth0"}}}
	if err := spec.Validate(); err != nil {
		t.Error(err)
	}
}

func TestSecurityOrDefaultResolvesTheUnsetValue(t *testing.T) {
	// A manifest that names only an SSID asks for the common case,
	// and an open network is the deviation a person spells out.
	cases := map[WirelessSecurity]WirelessSecurity{
		"":             WirelessWPAPSK,
		WirelessWPAPSK: WirelessWPAPSK,
		WirelessOpen:   WirelessOpen,
	}
	for declared, want := range cases {
		t.Run(string(declared), func(t *testing.T) {
			got := WirelessSpec{SSID: "stonypoint", Security: declared}.SecurityOrDefault()
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestLoadParsesAWirelessInterface(t *testing.T) {
	path := writeManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: stick-1
spec:
  network:
    interfaces:
      - name: eth0
      - name: wlan0
        address: 10.10.0.7/24
        wireless:
          ssid: stonypoint
          security: wpa-psk
`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	interfaces := m.Spec.Network.Interfaces
	if interfaces[0].Wireless != nil {
		t.Errorf("a wired entry declares no wireless: %+v", interfaces[0].Wireless)
	}
	wireless := interfaces[1].Wireless
	if wireless == nil {
		t.Fatalf("wlan0 declared a wireless network: %+v", interfaces[1])
	}
	if wireless.SSID != "stonypoint" || wireless.Security != WirelessWPAPSK {
		t.Errorf("wlan0 wireless: got %+v", wireless)
	}
}

func TestLoadRejectsAPassphraseInTheManifest(t *testing.T) {
	// The manifest travels on install sticks and in deployment git,
	// so strict parsing turns a field that looks like the place for
	// the secret into an error a person sees before the secret
	// travels anywhere.
	path := writeManifest(t, `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: stick-1
spec:
  network:
    interfaces:
      - name: wlan0
        wireless:
          ssid: stonypoint
          passphrase: hunter2
`)
	if _, err := Load(path); err == nil {
		t.Error("a wireless entry must have no field for a passphrase")
	}
}

// machineCRDWireless is the wireless schema as the CRD carries it,
// so the tests can hold the API server's copy of the rules against
// this package's copy.
type machineCRDWireless struct {
	Required   []string `json:"required"`
	Properties struct {
		SSID struct {
			Type      string `json:"type"`
			MaxLength int    `json:"maxLength"`
			Pattern   string `json:"pattern"`
		} `json:"ssid"`
		Security struct {
			Type    string   `json:"type"`
			Enum    []string `json:"enum"`
			Default string   `json:"default"`
		} `json:"security"`
	} `json:"properties"`
}

// readMachineCRDWireless reads the wireless schema out of the CRD
// manifest.
func readMachineCRDWireless(t *testing.T) machineCRDWireless {
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
									Network struct {
										Properties struct {
											Interfaces struct {
												Items struct {
													Properties struct {
														Wireless machineCRDWireless `json:"wireless"`
													} `json:"properties"`
												} `json:"items"`
											} `json:"interfaces"`
										} `json:"properties"`
									} `json:"network"`
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
	return crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Spec.
		Properties.Network.Properties.Interfaces.Items.Properties.Wireless
}

func TestMachineCRDDeclaresTheWirelessFields(t *testing.T) {
	wireless := readMachineCRDWireless(t)
	if !slices.Contains(wireless.Required, "ssid") {
		t.Errorf("a wireless entry names a network, so ssid is required: %v", wireless.Required)
	}
	if slices.Contains(wireless.Required, "security") {
		t.Errorf("security defaults, so it is not required: %v", wireless.Required)
	}
	if wireless.Properties.SSID.Type != "string" || wireless.Properties.SSID.MaxLength != 32 {
		t.Errorf("ssid: got %+v", wireless.Properties.SSID)
	}
	security := wireless.Properties.Security
	if !slices.Equal(security.Enum, []string{string(WirelessWPAPSK), string(WirelessOpen)}) {
		t.Errorf("security enum: got %v", security.Enum)
	}
	if security.Default != string(WirelessWPAPSK) {
		t.Errorf("security default: got %q", security.Default)
	}
}

func TestTheCRDPatternJudgesAnSSIDTheSameWayValidateDoes(t *testing.T) {
	// The API server runs the pattern and never runs the Go check,
	// so a spec applied with kubectl passes only the pattern. The
	// two answer alike on the file-naming rule. The control-byte
	// rows are the deliberate gap, and the operator closes it when
	// it refuses to stage.
	pattern, err := regexp.Compile(readMachineCRDWireless(t).Properties.SSID.Pattern)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range ssidCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pattern.MatchString(tc.ssid); got != tc.matches {
				t.Errorf("the pattern accepts %q: %v, want %v", tc.ssid, got, tc.matches)
			}
		})
	}
}
