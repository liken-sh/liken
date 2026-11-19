package machine

// A manifest has no field for a Wi-Fi passphrase, because manifests
// are copied onto install sticks and committed to git. The passphrase
// is in the file /etc/liken/psk/<ssid> on the machine.

import (
	"fmt"
	"strings"
)

// WirelessSpec is the network that a radio joins and how it joins it.
// Neither field is secret, because an access point broadcasts its SSID.
type WirelessSpec struct {
	// SSID is the network's name. It is also the name of the
	// passphrase file, so validate refuses an SSID that cannot be a
	// file name.
	SSID string `json:"ssid"`

	// Security is how the radio joins. An unset value means wpa-psk.
	Security WirelessSecurity `json:"security,omitempty"`
}

// WirelessSecurity is how a machine joins a network. wpa-psk covers
// both WPA2 and WPA3 personal, because the supplicant negotiates the
// protocol with each access point.
type WirelessSecurity string

const (
	WirelessWPAPSK WirelessSecurity = "wpa-psk"
	WirelessOpen   WirelessSecurity = "open"
)

// SecurityOrDefault returns the security of the spec, or wpa-psk when
// it is unset. The CRD sets the same default, but a manifest from an
// install stick never passes through the API server.
func (w WirelessSpec) SecurityOrDefault() WirelessSecurity {
	if w.Security == WirelessOpen {
		return WirelessOpen
	}
	return WirelessWPAPSK
}

// maxSSIDOctets is the largest SSID that the 802.11 SSID element
// allows. It counts octets, not characters.
const maxSSIDOctets = 32

// isControlByte reports whether r is a C0 control character or DEL.
func isControlByte(r rune) bool {
	return r < 0x20 || r == 0x7f
}

func (w WirelessSpec) validate(name string) error {
	// The SSID must name a file in /etc/liken/psk. An empty name, "."
	// and ".." name directories, and a slash names a file outside it.
	switch {
	case w.SSID == "":
		return fmt.Errorf("interface %s declares a wireless network with no ssid, and the ssid is both the network name and the name of its passphrase file in /etc/liken/psk", name)
	case w.SSID == "." || w.SSID == "..":
		return fmt.Errorf("interface %s declares the ssid %q, which is a directory name, so it cannot name a passphrase file in /etc/liken/psk", name, w.SSID)
	case strings.Contains(w.SSID, "/"):
		return fmt.Errorf("interface %s declares the ssid %q, which has a path separator, so it names a file outside /etc/liken/psk", name, w.SSID)
	// 802.11 allows any octet in an SSID, but a newline would split
	// the SSID's line in the facts tree and in the generated
	// supplicant configuration, and a NUL would end the passphrase
	// path early.
	case strings.ContainsFunc(w.SSID, isControlByte):
		return fmt.Errorf("interface %s declares the ssid %q, which has a control byte, and an ssid may hold printable characters only", name, w.SSID)
	}
	if len(w.SSID) > maxSSIDOctets {
		return fmt.Errorf("interface %s declares an ssid of %d octets, and 802.11 allows at most %d",
			name, len(w.SSID), maxSSIDOctets)
	}
	// Init has no supplicant setting for an unknown value, and on the
	// machine the join would fail with no shell to look into it. So
	// validate refuses the value before the spec reaches the machine.
	switch w.Security {
	case "", WirelessWPAPSK, WirelessOpen:
		return nil
	default:
		return fmt.Errorf("interface %s declares the security %q, and the allowed values are %s and %s",
			name, w.Security, WirelessWPAPSK, WirelessOpen)
	}
}
