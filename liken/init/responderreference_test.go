package main

import (
	"bytes"
	"crypto/md5"
	"net"
	"testing"
	"time"
)

// clockSyncedTo is a clock whose last measurement came from source.
func clockSyncedTo(source string) *clock {
	clk := newClock([]string{source})
	clk.record(&timeSync{source: source, stratum: 2, at: time.Now()})
	return clk
}

// clientRequest is an NTPv4 client request.
func clientRequest() []byte {
	request := make([]byte, 48)
	request[0] = 4<<3 | modeClient
	return request
}

// RFC 5905, section 7.3: above stratum 1, the reference ID is the
// upstream server's IPv4 address, or the first four bytes of the MD5
// hash of its IPv6 address. A client compares it with its own address
// to detect a timing loop.
func TestTheReferenceIDNamesTheUpstreamServer(t *testing.T) {
	ipv6 := md5.Sum(net.ParseIP("2001:db8::123"))
	for _, tc := range []struct {
		name   string
		source string
		want   []byte
	}{
		{"an IPv4 source", "192.0.2.123", []byte{192, 0, 2, 123}},
		{"an IPv6 source", "2001:db8::123", ipv6[:4]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := respondTo(clientRequest(), clockSyncedTo(tc.source), time.Now())
			if got := reply[12:16]; !bytes.Equal(got, tc.want) {
				t.Errorf("the reference ID for the source %s is % x, want % x", tc.source, got, tc.want)
			}
		})
	}
}

// A source configured as a hostname is measured through the address
// that answered, so the reference ID names that address. The dial
// sends the query for time.example.com to a responder in the test.
func TestTheReferenceIDOfAHostnameSourceNamesTheAddressThatAnswered(t *testing.T) {
	responder := startResponder(t, syncedClock())
	orig := dialTimeSource
	dialTimeSource = func(local, remote string) (net.Conn, error) {
		return orig(local, responder)
	}
	t.Cleanup(func() { dialTimeSource = orig })

	sync, err := querySources([]string{"time.example.com"})
	if err != nil {
		t.Fatalf("querying time.example.com through the responder at %s: %v", responder, err)
	}
	clk := newClock([]string{"time.example.com"})
	clk.record(sync)
	reply := respondTo(clientRequest(), clk, time.Now())
	if got, want := reply[12:16], []byte{127, 0, 0, 1}; !bytes.Equal(got, want) {
		t.Errorf("the reference ID for the source time.example.com is % x, want % x, the address that answered", got, want)
	}
}
