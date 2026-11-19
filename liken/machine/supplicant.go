package machine

// A client for the control socket of wpa_supplicant. init runs one
// wpa_supplicant for each radio and reads its events to join the
// network (init/wpactrl.go). machine-operator sends SIGNAL_POLL to the
// same socket on each Prometheus scrape, because the signal strength
// changes too often to store (machine-operator/wirelessmetrics.go).

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// WirelessDir is the directory of each radio's supplicant configuration
// and control socket. It is on the /run tmpfs because the configuration contains
// the network's passphrase. machine-operator mounts /run/liken from
// the host, so it opens the sockets at the same path.
const WirelessDir = "/run/liken/wireless"

// SupplicantSocket returns the path of the control socket of the
// supplicant for interface ifname under dir.
func SupplicantSocket(dir, ifname string) string {
	return filepath.Join(dir, ifname, "ctrl", ifname)
}

// supplicantReplyMax is the size of the reply buffer, the same size
// that wpa_cli uses.
const supplicantReplyMax = 4096

// requests numbers the client sockets, so concurrent requests from
// one process bind different addresses.
var requests atomic.Uint64

// AskSupplicant sends one command to a supplicant's control socket and
// returns the key=value lines of the reply. STATUS and SIGNAL_POLL
// reply in that format.
//
// The control socket is a datagram socket, and the supplicant sends
// its reply to the address that the request came from, so the client
// must bind its own address. The address is in the abstract namespace,
// so there is no socket file to remove afterward. Abstract addresses
// belong to a network namespace, and init and machine-operator both
// run in the host's. The client never sends ATTACH, so the supplicant
// sends it no events that could arrive in place of the reply.
func AskSupplicant(socket, command string, timeout time.Duration) (map[string]string, error) {
	local := fmt.Sprintf("@liken-supplicant-%d-%d", os.Getpid(), requests.Add(1))
	conn, err := net.DialUnix("unixgram",
		&net.UnixAddr{Name: local, Net: "unixgram"},
		&net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", socket, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	if _, err := conn.Write([]byte(command)); err != nil {
		return nil, fmt.Errorf("sending %s to %s: %w", command, socket, err)
	}
	buf := make([]byte, supplicantReplyMax)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("reading the reply to %s from %s: %w", command, socket, err)
	}
	reply := strings.TrimSpace(string(buf[:n]))
	if reply == "FAIL" || reply == "UNKNOWN COMMAND" {
		return nil, fmt.Errorf("the supplicant at %s replied to %s with %s", socket, command, reply)
	}
	fields := map[string]string{}
	for line := range strings.SplitSeq(reply, "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			fields[key] = value
		}
	}
	return fields, nil
}

// SupplicantAssociation is the access point in a supplicant's STATUS
// reply. The zero value means the radio is not associated.
type SupplicantAssociation struct {
	BSSID     string
	Frequency int
}

// AskSupplicantAssociation returns the access point that the radio is
// associated with. A STATUS reply can have a bssid while the
// supplicant is still in the handshake, so the function returns the
// zero value unless wpa_state is COMPLETED.
func AskSupplicantAssociation(socket string, timeout time.Duration) (SupplicantAssociation, error) {
	status, err := AskSupplicant(socket, "STATUS", timeout)
	if err != nil {
		return SupplicantAssociation{}, err
	}
	if status["wpa_state"] != "COMPLETED" {
		return SupplicantAssociation{}, nil
	}
	frequency, _ := strconv.Atoi(status["freq"])
	return SupplicantAssociation{BSSID: status["bssid"], Frequency: frequency}, nil
}

// WirelessBand returns the 802.11 band of a channel's center frequency
// in MHz, or "" for a frequency outside every band. The 6 GHz band
// starts at 5925 MHz, where the 5 GHz band ends.
func WirelessBand(mhz int) string {
	switch {
	case mhz >= 2400 && mhz < 2500:
		return "2.4GHz"
	case mhz >= 5150 && mhz < 5925:
		return "5GHz"
	case mhz >= 5925 && mhz <= 7125:
		return "6GHz"
	case mhz >= 57000 && mhz <= 71000:
		return "60GHz"
	}
	return ""
}

// disconnectReasons maps the IEEE 802.11 reason codes that a radio
// commonly reports when it loses an access point to a description
// (IEEE 802.11-2020, table 9-49). The table is not complete.
var disconnectReasons = map[int]string{
	1:  "unspecified",
	2:  "the previous authentication is no longer valid",
	3:  "deauthenticated because the sender is leaving",
	4:  "disassociated because of inactivity",
	5:  "the access point cannot handle all its associated stations",
	6:  "a class 2 frame from an unauthenticated station",
	7:  "a class 3 frame from an unassociated station",
	8:  "disassociated because the sender is leaving",
	9:  "the station is not authenticated",
	14: "a message integrity check failed",
	15: "the 4-way handshake timed out",
	16: "the group key handshake timed out",
	23: "IEEE 802.1X authentication failed",
	34: "too many frames were not acknowledged",
}

// DisconnectReasonText formats an 802.11 reason code as its number,
// followed by its description when disconnectReasons has one.
func DisconnectReasonText(code int) string {
	if text, ok := disconnectReasons[code]; ok {
		return fmt.Sprintf("reason %d, %s", code, text)
	}
	return fmt.Sprintf("reason %d", code)
}

// statusCodes maps the IEEE 802.11 status codes that an access point
// commonly returns when it refuses an authentication or an association
// to a description (IEEE 802.11-2020, table 9-50). The descriptions
// follow wpa_supplicant's WLAN_STATUS_ constants. The table is not
// complete.
var statusCodes = map[int]string{
	1:   "unspecified failure",
	10:  "the access point cannot support all the requested capabilities",
	12:  "the association was denied for a reason outside the standard",
	13:  "the access point does not support the authentication algorithm",
	15:  "the challenge failed",
	16:  "the authentication timed out",
	17:  "the access point cannot handle another station",
	30:  "the access point rejected the association temporarily, and the station can try again later",
	37:  "the request was declined",
	53:  "the PMKID is not valid",
	72:  "the RSN element is not valid",
	76:  "an anti-clogging token is required",
	77:  "the access point does not support the SAE group",
	126: "the access point requires SAE hash-to-element",
}

// StatusCodeText formats an 802.11 status code as its number,
// followed by its description when statusCodes has one.
func StatusCodeText(code int) string {
	if text, ok := statusCodes[code]; ok {
		return fmt.Sprintf("status %d, %s", code, text)
	}
	return fmt.Sprintf("status %d", code)
}
