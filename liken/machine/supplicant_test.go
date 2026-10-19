package machine

import (
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/machine/supplicanttest"
)

// statusCompleted is the answer a real wpa_supplicant 2.12 gave to
// STATUS on an associated radio.
const statusCompleted = `bssid=04:4a:2c:11:22:33
freq=5180
ssid=homenet
id=0
mode=station
wifi_generation=6
pairwise_cipher=CCMP
group_cipher=CCMP
key_mgmt=SAE
wpa_state=COMPLETED
ip_address=10.0.0.12
address=a0:b1:c2:d3:e4:f5
uuid=0f3c1e8a-1f2b-5d4e-9a6b-7c8d9e0f1a2b
`

func TestASupplicantReportsTheAccessPointItIsAssociatedWith(t *testing.T) {
	s := supplicanttest.Start(t, t.TempDir(), "wlan0")
	s.Answer("STATUS", statusCompleted)
	got, err := AskSupplicantAssociation(s.Socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := SupplicantAssociation{BSSID: "04:4a:2c:11:22:33", Frequency: 5180}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestASupplicantStillJoiningReportsNoAccessPoint(t *testing.T) {
	s := supplicanttest.Start(t, t.TempDir(), "wlan0")
	s.Answer("STATUS", "bssid=04:4a:2c:11:22:33\nfreq=5180\nwpa_state=4WAY_HANDSHAKE\n")
	got, err := AskSupplicantAssociation(s.Socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got != (SupplicantAssociation{}) {
		t.Errorf("got %+v, want no access point before the handshake completes", got)
	}
}

func TestAnUnknownCommandIsAnError(t *testing.T) {
	s := supplicanttest.Start(t, t.TempDir(), "wlan0")
	if _, err := AskSupplicant(s.Socket, "SIGNAL_POLL", time.Second); err == nil {
		t.Error("an answer of UNKNOWN COMMAND must be an error")
	}
}

func TestAMissingSocketIsAnError(t *testing.T) {
	if _, err := AskSupplicant(SupplicantSocket(t.TempDir(), "wlan0"), "STATUS", time.Second); err == nil {
		t.Error("a socket that does not exist must be an error")
	}
}

func TestWirelessBandNamesTheBandOfAFrequency(t *testing.T) {
	cases := []struct {
		mhz  int
		want string
	}{
		{2412, "2.4GHz"},
		{2484, "2.4GHz"},
		{5180, "5GHz"},
		{5885, "5GHz"},
		{5955, "6GHz"},
		{7115, "6GHz"},
		{58320, "60GHz"},
		{0, ""},
		{900, ""},
	}
	for _, c := range cases {
		if got := WirelessBand(c.mhz); got != c.want {
			t.Errorf("WirelessBand(%d) = %q, want %q", c.mhz, got, c.want)
		}
	}
}

func TestDisconnectReasonTextNamesAKnownCode(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{4, "reason 4, disassociated because of inactivity"},
		{3, "reason 3, deauthenticated because the sender is leaving"},
		{99, "reason 99"},
	}
	for _, c := range cases {
		if got := DisconnectReasonText(c.code); got != c.want {
			t.Errorf("DisconnectReasonText(%d) = %q, want %q", c.code, got, c.want)
		}
	}
}
