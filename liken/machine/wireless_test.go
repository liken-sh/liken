package machine

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// ssidCase is one SSID, whether Validate accepts it (valid), and
// whether the CRD pattern accepts it (matches). They differ only for
// control bytes, which Validate refuses and the pattern accepts.
type ssidCase struct {
	name    string
	ssid    string
	valid   bool
	matches bool
}

var ssidCases = []ssidCase{
	{name: "a plain name", ssid: "homenet", valid: true, matches: true},
	{name: "spaces and punctuation", ssid: "Guest Net 5G!", valid: true, matches: true},
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
				t.Errorf("Validate() for the ssid %q returned %v, want no error", tc.ssid, err)
			}
			if !tc.valid && err == nil {
				t.Errorf("Validate() for the ssid %q returned no error, want a refusal", tc.ssid)
			}
		})
	}
}

func TestNetworkValidateEscapesAControlByteInTheSSID(t *testing.T) {
	// The raw byte would not be readable in the message, so the error
	// must write it as an escape.
	err := wirelessSpec("home\nguest", WirelessWPAPSK).Validate()
	if err == nil {
		t.Fatal("Validate() for an ssid with a newline returned no error, want a refusal")
	}
	if !strings.Contains(err.Error(), "wlan0") {
		t.Errorf("the error is %q, want it to name wlan0", err)
	}
	if !strings.Contains(err.Error(), `\n`) {
		t.Errorf("the error is %q, want the newline written as an escape because a raw newline is unreadable", err)
	}
}

func TestNetworkValidateAcceptsAnSSIDOf32Octets(t *testing.T) {
	if err := wirelessSpec(strings.Repeat("s", 32), WirelessWPAPSK).Validate(); err != nil {
		t.Error(err)
	}
}

func TestNetworkValidateRejectsAnSSIDLongerThan32Octets(t *testing.T) {
	err := wirelessSpec(strings.Repeat("s", 33), WirelessWPAPSK).Validate()
	if err == nil {
		t.Fatal("Validate() for an ssid of 33 octets returned no error, want a refusal")
	}
	if !strings.Contains(err.Error(), "32") {
		t.Errorf("the error is %q, want it to name the limit 32", err)
	}
}

func TestNetworkValidateCountsAnSSIDInOctetsNotCharacters(t *testing.T) {
	if err := wirelessSpec(strings.Repeat("é", 16), WirelessWPAPSK).Validate(); err != nil {
		t.Errorf("Validate() for 16 two-octet characters (32 octets) returned %v, want no error", err)
	}
	if err := wirelessSpec(strings.Repeat("é", 17), WirelessWPAPSK).Validate(); err == nil {
		t.Error("Validate() for 17 two-octet characters (34 octets) returned no error, want a refusal")
	}
}

func TestNetworkValidateAcceptsBothSecurityValues(t *testing.T) {
	for _, security := range []WirelessSecurity{WirelessWPAPSK, WirelessOpen, ""} {
		t.Run(string(security), func(t *testing.T) {
			if err := wirelessSpec("homenet", security).Validate(); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestNetworkValidateRejectsAnUnknownSecurityValue(t *testing.T) {
	// A manifest from an install stick reaches init without the CRD's
	// enum check.
	err := wirelessSpec("homenet", "wep").Validate()
	if err == nil {
		t.Fatal("Validate() for the security wep returned no error, want a refusal")
	}
	if !strings.Contains(err.Error(), "wep") {
		t.Errorf("the error is %q, want it to name the value wep", err)
	}
}

func TestNetworkValidateLeavesAWiredInterfaceAlone(t *testing.T) {
	spec := NetworkSpec{Interfaces: []InterfaceSpec{{Name: "eth0"}}}
	if err := spec.Validate(); err != nil {
		t.Error(err)
	}
}

func TestSecurityOrDefaultResolvesTheUnsetValue(t *testing.T) {
	cases := map[WirelessSecurity]WirelessSecurity{
		"":             WirelessWPAPSK,
		WirelessWPAPSK: WirelessWPAPSK,
		WirelessOpen:   WirelessOpen,
	}
	for declared, want := range cases {
		t.Run(string(declared), func(t *testing.T) {
			got := WirelessSpec{SSID: "homenet", Security: declared}.SecurityOrDefault()
			if got != want {
				t.Errorf("SecurityOrDefault() for %q is %q, want %q", declared, got, want)
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
          ssid: homenet
          security: wpa-psk
`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	interfaces := m.Spec.Network.Interfaces
	if interfaces[0].Wireless != nil {
		t.Errorf("the wired entry has wireless %+v, want nil", interfaces[0].Wireless)
	}
	wireless := interfaces[1].Wireless
	if wireless == nil {
		t.Fatalf("wlan0 is %+v, want a wireless network", interfaces[1])
	}
	if wireless.SSID != "homenet" || wireless.Security != WirelessWPAPSK {
		t.Errorf("wlan0 wireless is %+v, want ssid homenet and security wpa-psk", wireless)
	}
}

func TestLoadRejectsAPassphraseInTheManifest(t *testing.T) {
	// Strict parsing refuses the unknown field, so a passphrase cannot
	// be written into a manifest.
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
          ssid: homenet
          passphrase: hunter2
`)
	if _, err := Load(path); err == nil {
		t.Error("Load returned no error for a passphrase in a wireless entry, want a refusal of the unknown field")
	}
}

// machineCRDWireless is the wireless schema of the Machine CRD.
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
		t.Errorf("the required fields are %v, want ssid among them", wireless.Required)
	}
	if slices.Contains(wireless.Required, "security") {
		t.Errorf("the required fields are %v, want security absent because it has a default", wireless.Required)
	}
	if wireless.Properties.SSID.Type != "string" || wireless.Properties.SSID.MaxLength != 32 {
		t.Errorf("the ssid schema is %+v, want type string and maxLength 32", wireless.Properties.SSID)
	}
	security := wireless.Properties.Security
	if !slices.Equal(security.Enum, []string{string(WirelessWPAPSK), string(WirelessOpen)}) {
		t.Errorf("the security enum is %v, want wpa-psk and open", security.Enum)
	}
	if security.Default != string(WirelessWPAPSK) {
		t.Errorf("the security default is %q, want wpa-psk", security.Default)
	}
}

func TestTheCRDPatternJudgesAnSSIDTheSameWayValidateDoes(t *testing.T) {
	// The API server checks a spec applied with kubectl against the
	// pattern only. The pattern accepts control bytes, and the machine
	// operator refuses such a spec when it runs Validate.
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
