package machine

// The supplicant's control socket, as both sides of the machine use
// it. init runs one wpa_supplicant for each radio and reads its events
// for the join (init/wpactrl.go). machine-operator asks the same
// socket for the signal when Prometheus scrapes, because the signal
// changes all the time and a request at scrape time reads it only
// when Prometheus asks for it (machine-operator/wirelessmetrics.go).

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

// WirelessDir holds each radio's supplicant configuration and control
// socket. It is on /run, a tmpfs, because the configuration holds the
// network's passphrase. machine-operator mounts /run/liken, so it
// reaches the sockets at the same path.
const WirelessDir = "/run/liken/wireless"

// SupplicantSocket is the control socket of one radio's supplicant
// under dir. Each interface has its own directory, so the socket's
// name in it is always the interface's name.
func SupplicantSocket(dir, ifname string) string {
	return filepath.Join(dir, ifname, "ctrl", ifname)
}

// supplicantReplyMax is the largest reply read. The answer to STATUS
// is about 400 bytes, and upstream's own client reads into a buffer of
// this size.
const supplicantReplyMax = 4096

// requests numbers each request socket, so two requests at once from
// one process never bind the same address.
var requests atomic.Uint64

// AskSupplicant sends one command to a supplicant's control socket and
// answers the key=value lines of its reply. STATUS and SIGNAL_POLL
// answer in that form.
//
// The socket is a datagram socket, and the supplicant sends its reply
// to the address the request came from, so the client must bind an
// address. The address is in the abstract namespace, which has no
// file to remove afterward. Both init and machine-operator run in the
// host's network namespace, which is the namespace the abstract
// address lives in. This socket never sends ATTACH, so no event
// arrives on it to mix with the reply.
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
		return nil, fmt.Errorf("reading the answer to %s from %s: %w", command, socket, err)
	}
	reply := strings.TrimSpace(string(buf[:n]))
	if reply == "FAIL" || reply == "UNKNOWN COMMAND" {
		return nil, fmt.Errorf("the supplicant at %s answered %s with %s", socket, command, reply)
	}
	fields := map[string]string{}
	for line := range strings.SplitSeq(reply, "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			fields[key] = value
		}
	}
	return fields, nil
}

// SupplicantAssociation is the access point a supplicant reports in
// its answer to STATUS. A zero value means the radio is not
// associated.
type SupplicantAssociation struct {
	BSSID     string
	Frequency int
}

// AskSupplicantAssociation asks a supplicant which access point its
// radio is associated with. wpa_state is COMPLETED only after the
// handshake finishes, and an earlier state can already name the BSSID
// it is trying, so the BSSID counts only in that state.
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

// WirelessBand names the band of a channel's center frequency in MHz,
// or the empty string for a frequency outside the bands that 802.11
// uses. The 6 GHz band starts at 5925 MHz, where the 5 GHz band ends.
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

// disconnectReasons gives the IEEE 802.11 reason codes that a radio
// commonly reports when it loses an access point (IEEE 802.11-2020,
// table 9-49). A code outside the table prints as its number alone.
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

// DisconnectReasonText renders an 802.11 reason code as its number
// and, when the table holds it, its meaning.
func DisconnectReasonText(code int) string {
	if text, ok := disconnectReasons[code]; ok {
		return fmt.Sprintf("reason %d, %s", code, text)
	}
	return fmt.Sprintf("reason %d", code)
}

// statusCodes gives the IEEE 802.11 status codes that an access point
// commonly answers when it refuses an authentication or an association
// (IEEE 802.11-2020, table 9-50). The names follow upstream's
// WLAN_STATUS_ constants. A code outside the table prints as its number
// alone.
var statusCodes = map[int]string{
	1:   "unspecified failure",
	10:  "the access point cannot support all the requested capabilities",
	12:  "the association was denied for a reason outside the standard",
	13:  "the access point does not support the authentication algorithm",
	15:  "the challenge failed",
	16:  "the authentication timed out",
	17:  "the access point cannot handle another station",
	30:  "the association was rejected for now; try again later",
	37:  "the request was declined",
	53:  "the PMKID is not valid",
	72:  "the RSN element is not valid",
	76:  "an anti-clogging token is required",
	77:  "the access point does not support the SAE group",
	126: "the access point requires SAE hash-to-element",
}

// StatusCodeText renders an 802.11 status code as its number and, when
// the table holds it, its meaning.
func StatusCodeText(code int) string {
	if text, ok := statusCodes[code]; ok {
		return fmt.Sprintf("status %d, %s", code, text)
	}
	return fmt.Sprintf("status %d", code)
}
