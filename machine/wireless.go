package machine

// The wireless half of an interface: what a manifest may say about a
// radio. The spec carries only public facts, the network's name and
// how the machine joins it. The passphrase is deliberately absent. A
// manifest travels on install sticks and in deployment git, so the
// secret lives in a file on the machine instead, and the SSID is the
// key that finds it (plans/62-wifi.md).

import (
	"fmt"
	"strings"
)

// WirelessSpec names the network a radio joins and how it joins it.
// Both fields are public facts. An access point broadcasts its SSID
// to anyone listening, so writing it in a manifest reveals nothing.
type WirelessSpec struct {
	// SSID is the network's name, as the access point advertises
	// it. The name doubles as the name of the passphrase file at
	// /etc/liken/psk/<ssid>, which is why validate refuses an SSID
	// that cannot name a file.
	SSID string `json:"ssid"`

	// Security is how the radio joins. An unset value means
	// wpa-psk, the common case. open is the deviation a person
	// spells out.
	Security WirelessSecurity `json:"security,omitempty"`
}

// WirelessSecurity is the way a machine joins a network. wpa-psk
// covers WPA2 and WPA3 personal with one passphrase, because the
// supplicant negotiates the protocol with each access point.
type WirelessSecurity string

const (
	WirelessWPAPSK WirelessSecurity = "wpa-psk"
	WirelessOpen   WirelessSecurity = "open"
)

// SecurityOrDefault names the security an unset field asks for. The
// CRD writes the default into a spec applied through the API server,
// but a manifest carried in on a stick never met the API server, so
// both readers resolve the value here. rebootPolicy takes the same
// two-path treatment.
func (w WirelessSpec) SecurityOrDefault() WirelessSecurity {
	if w.Security == WirelessOpen {
		return WirelessOpen
	}
	return WirelessWPAPSK
}

// maxSSIDOctets is the largest SSID that 802.11 carries in one
// information element. The limit counts octets, not characters.
const maxSSIDOctets = 32

// isControlByte reports whether a rune is one of the C0 controls or
// DEL. These are the bytes that the SSID's consumers read as
// structure rather than as text: the facts tree writes the SSID as
// one line of a file, and init renders it into a generated
// supplicant configuration.
func isControlByte(r rune) bool {
	return r < 0x20 || r == 0x7f
}

// validate checks one interface's wireless entry. Each rule states
// its reason at the point where it refuses.
func (w WirelessSpec) validate(name string) error {
	// Init reads the passphrase from /etc/liken/psk/<ssid>, so the
	// SSID has to be a name a path can end in. These values are the
	// ones that are not: the empty name opens the directory itself,
	// the two dot names open a directory beside it, and a separator
	// opens a file somewhere else entirely.
	switch {
	case w.SSID == "":
		return fmt.Errorf("interface %s declares a wireless network with no ssid; the ssid names both the network and the file that holds its passphrase", name)
	case w.SSID == "." || w.SSID == "..":
		return fmt.Errorf("interface %s declares the ssid %q, which names a directory and not a passphrase file", name, w.SSID)
	case strings.Contains(w.SSID, "/"):
		return fmt.Errorf("interface %s declares the ssid %q, which holds a path separator and so names a file outside /etc/liken/psk", name, w.SSID)
	// 802.11 lets an SSID carry any octet, but every consumer of
	// the name here reads lines: an embedded newline would split the
	// facts tree's one-line record and end a generated configuration
	// line early. A NUL falls to the same rule, because it would end
	// the passphrase path before the name does. No manifest has a
	// legitimate reason to spell any of these bytes.
	case strings.ContainsFunc(w.SSID, isControlByte):
		return fmt.Errorf("interface %s declares the ssid %q, which holds a control byte; an ssid holds printable characters only", name, w.SSID)
	}
	if len(w.SSID) > maxSSIDOctets {
		return fmt.Errorf("interface %s declares an ssid of %d octets; 802.11 carries at most %d",
			name, len(w.SSID), maxSSIDOctets)
	}
	// Init renders the security into a supplicant configuration. A
	// word it has no rendering for must be refused here, where the
	// operator can read the answer, because on the machine it would
	// become a failed join with no shell to investigate from.
	switch w.Security {
	case "", WirelessWPAPSK, WirelessOpen:
		return nil
	default:
		return fmt.Errorf("interface %s declares the security %q; the values are %s and %s",
			name, w.Security, WirelessWPAPSK, WirelessOpen)
	}
}
