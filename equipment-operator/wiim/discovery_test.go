// Tests of the discovery parsers and the merge, and of the SSDP and
// mDNS searches against in-memory responders. A search sends from a
// packetConn instead of a multicast socket, and a responder replies
// with canned packets on the fake clock of a synctest bubble.

package wiim

import (
	"context"
	"net"
	"os"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// packetConn is an in-memory socket for a search. It passes each packet
// that the search sends to respond on a new goroutine, so a responder
// can delay its reply as a renderer does up to the MX. ReadFrom waits
// for a reply, the read deadline, or Close.
type packetConn struct {
	respond func(conn *packetConn, packet []byte, to net.Addr)
	inbox   chan []byte
	closed  chan struct{}
	once    sync.Once

	mutex    sync.Mutex
	deadline time.Time
}

// usePackets makes every socket that a search opens a packetConn that
// calls respond, until the test ends.
func usePackets(t *testing.T, respond func(conn *packetConn, packet []byte, to net.Addr)) {
	t.Helper()
	restore := listenPacket
	listenPacket = func(string, string) (net.PacketConn, error) {
		return &packetConn{respond: respond, inbox: make(chan []byte, 16), closed: make(chan struct{})}, nil
	}
	t.Cleanup(func() { listenPacket = restore })
}

// answer queues a packet for ReadFrom to return. After Close it drops
// the packet.
func (c *packetConn) answer(packet []byte) {
	select {
	case c.inbox <- packet:
	case <-c.closed:
	}
}

func (c *packetConn) WriteTo(packet []byte, to net.Addr) (int, error) {
	go c.respond(c, append([]byte(nil), packet...), to)
	return len(packet), nil
}

func (c *packetConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	c.mutex.Lock()
	deadline := c.deadline
	c.mutex.Unlock()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case packet := <-c.inbox:
		return copy(buffer, packet), &net.UDPAddr{IP: net.IPv4(192, 0, 2, 10), Port: 1900}, nil
	case <-timer.C:
		return 0, nil, os.ErrDeadlineExceeded
	case <-c.closed:
		return 0, nil, net.ErrClosed
	}
}

func (c *packetConn) SetReadDeadline(deadline time.Time) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.deadline = deadline
	return nil
}

func (c *packetConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *packetConn) LocalAddr() net.Addr                  { return &net.UDPAddr{IP: net.IPv4zero, Port: 40000} }
func (c *packetConn) SetDeadline(deadline time.Time) error { return c.SetReadDeadline(deadline) }
func (c *packetConn) SetWriteDeadline(time.Time) error     { return nil }

// mdnsDevicePacket builds an mDNS response for an instance named "WiiM
// Amp". With includeA false the packet has no A record, so a test can
// send the address in a separate packet.
func mdnsDevicePacket(t *testing.T, includeA bool) []byte {
	t.Helper()
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true})
	if err := builder.StartAnswers(); err != nil {
		t.Fatalf("starting the mDNS answers: %v", err)
	}
	header := func(name string) dnsmessage.ResourceHeader {
		return dnsmessage.ResourceHeader{
			Name:  dnsmessage.MustNewName(name),
			Class: dnsmessage.ClassINET,
			TTL:   120,
		}
	}
	if err := builder.PTRResource(
		header(linkplayService),
		dnsmessage.PTRResource{PTR: dnsmessage.MustNewName("WiiM Amp._linkplay._tcp.local.")},
	); err != nil {
		t.Fatalf("packing the mDNS PTR record: %v", err)
	}
	if err := builder.SRVResource(
		header("WiiM Amp._linkplay._tcp.local."),
		dnsmessage.SRVResource{Port: 49152, Target: dnsmessage.MustNewName("WiiMAmp.local.")},
	); err != nil {
		t.Fatalf("packing the mDNS SRV record: %v", err)
	}
	if err := builder.TXTResource(
		header("WiiM Amp._linkplay._tcp.local."),
		dnsmessage.TXTResource{TXT: []string{"uuid=ff98f2f7-aabb-ccdd-eeff-0011ff98f2f7"}},
	); err != nil {
		t.Fatalf("packing the mDNS TXT record: %v", err)
	}
	if includeA {
		if err := builder.AResource(
			header("WiiMAmp.local."),
			dnsmessage.AResource{A: [4]byte{192, 0, 2, 10}},
		); err != nil {
			t.Fatalf("packing the mDNS A record: %v", err)
		}
	}
	packet, err := builder.Finish()
	if err != nil {
		t.Fatalf("finishing the mDNS packet: %v", err)
	}
	return packet
}

// mdnsARecordPacket builds a packet with only the A record for the host
// of mdnsDevicePacket, as a responder sends when it splits its answer.
func mdnsARecordPacket(t *testing.T) []byte {
	t.Helper()
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true})
	if err := builder.StartAnswers(); err != nil {
		t.Fatalf("starting the mDNS answers: %v", err)
	}
	if err := builder.AResource(
		dnsmessage.ResourceHeader{
			Name:  dnsmessage.MustNewName("WiiMAmp.local."),
			Class: dnsmessage.ClassINET,
			TTL:   120,
		},
		dnsmessage.AResource{A: [4]byte{192, 0, 2, 10}},
	); err != nil {
		t.Fatalf("packing the mDNS A record: %v", err)
	}
	packet, err := builder.Finish()
	if err != nil {
		t.Fatalf("finishing the mDNS packet: %v", err)
	}
	return packet
}

// parseMDNS turns a packet with PTR, SRV, TXT, and A records into one
// device.
func TestParseMDNSBuildsADevice(t *testing.T) {
	instances := map[string]*mdnsInstance{}
	parseMDNS(mdnsDevicePacket(t, true), instances)
	devices := assembleMDNS(instances)

	mustMatch(t, len(devices), 1)
	mustMatch(t, devices[0].UUID, "FF98F2F7AABBCCDDEEFF0011")
	mustMatch(t, devices[0].Name, "WiiM Amp")
	mustMatch(t, devices[0].Address, "192.0.2.10")
	mustMatch(t, devices[0].Description, "http://192.0.2.10:49152/description.xml")
}

// An instance is not a device until its address arrives in a later
// packet.
func TestParseMDNSAssemblesAcrossPackets(t *testing.T) {
	instances := map[string]*mdnsInstance{}
	parseMDNS(mdnsDevicePacket(t, false), instances)
	mustMatch(t, assembleMDNS(instances), []Device{})

	parseMDNS(mdnsARecordPacket(t), instances)
	devices := assembleMDNS(instances)
	mustMatch(t, len(devices), 1)
	mustMatch(t, devices[0].UUID, "FF98F2F7AABBCCDDEEFF0011")
	mustMatch(t, devices[0].Address, "192.0.2.10")
}

// parseMDNS ignores a packet that is not a DNS message.
func TestParseMDNSIgnoresGarbage(t *testing.T) {
	instances := map[string]*mdnsInstance{}
	parseMDNS([]byte("not a dns packet"), instances)
	mustMatch(t, assembleMDNS(instances), []Device{})
}

// parseSSDP reads a response whose header names are lower-case.
func TestParseSSDPReadsLowerCaseHeaderNames(t *testing.T) {
	response := []byte("HTTP/1.1 200 OK\r\n" +
		"location: http://192.0.2.10:49152/device.xml\r\n" +
		"usn: uuid:FF98F2F7-AABB-CCDD-EEFF-0011FF98F2F7::urn:schemas-upnp-org:device:MediaRenderer:1\r\n\r\n")
	device, ok := parseSSDP(response)

	mustMatch(t, ok, true)
	mustMatch(t, device.UUID, "FF98F2F7AABBCCDDEEFF0011")
	mustMatch(t, device.Address, "192.0.2.10")
	mustMatch(t, device.Name, "")
	mustMatch(t, device.Description, "http://192.0.2.10:49152/device.xml")
}

// parseSSDP returns false for a response without a USN or a LOCATION.
func TestParseSSDPRejectsWithoutIdentityOrAddress(t *testing.T) {
	if _, ok := parseSSDP([]byte("HTTP/1.1 200 OK\r\nLOCATION: http://192.0.2.10/x\r\n\r\n")); ok {
		t.Fatal("parseSSDP accepted a response without a USN, want it refused")
	}
	if _, ok := parseSSDP([]byte("HTTP/1.1 200 OK\r\nUSN: uuid:FF98F2F7AABBCCDDEEFF0011\r\n\r\n")); ok {
		t.Fatal("parseSSDP accepted a response without a LOCATION, want it refused")
	}
}

// parseSSDP returns false for a MediaRenderer that is not a LinkPlay
// device, because it answers the same search.
func TestParseSSDPRejectsANonLinkPlayRenderer(t *testing.T) {
	response := []byte("HTTP/1.1 200 OK\r\n" +
		"LOCATION: http://192.0.2.20:60006/desc.xml\r\n" +
		"USN: uuid:508945DC-67F5-1B40-0080-000678F69EB2::urn:schemas-upnp-org:device:MediaRenderer:1\r\n\r\n")
	if _, ok := parseSSDP(response); ok {
		t.Fatal("parseSSDP accepted a MediaRenderer that is not a LinkPlay device, want it refused")
	}
}

func TestIsLinkPlayUUID(t *testing.T) {
	cases := []struct {
		name string
		uuid string
		want bool
	}{
		{"dashed", "FF98F2F7-AABB-CCDD-EEFF-0011FF98F2F7", true},
		{"bare", "FF98F2F7AABBCCDDEEFF0011FF98F2F7", true},
		{"lowercase", "ff98f2f7-aabb-ccdd-eeff-0011ff98f2f7", true},
		{"another vendor", "508945DC-67F5-1B40-0080-000678F69EB2", false},
		{"too short", "FF98F2F7AABBCCDDEEFF0011", false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, isLinkPlayUUID(one.uuid), one.want)
		})
	}
}

// mergeDevice keeps one device per UUID and fills an empty name or
// address from the other record.
func TestMergeFillsMissingFields(t *testing.T) {
	devices := map[string]Device{}
	// SSDP returns a UUID and an address, but no name.
	mergeDevice(devices, Device{UUID: "FF98F2F7AABBCCDDEEFF0011", Address: "192.0.2.11"})
	// mDNS returns the same UUID with a name.
	mergeDevice(devices, Device{UUID: "FF98F2F7AABBCCDDEEFF0011", Name: "Amp", Address: "192.0.2.10"})

	got := sorted(devices)
	mustMatch(t, len(got), 1)
	mustMatch(t, got[0].Name, "Amp")
	mustMatch(t, got[0].Address, "192.0.2.11")
}

// mergeDevice keeps the first description URL, and sets it from a later
// record when the first had none.
func TestMergeFillsTheDescription(t *testing.T) {
	devices := map[string]Device{}
	mergeDevice(devices, Device{UUID: "FF98F2F7AABBCCDDEEFF0011", Address: "192.0.2.10"})
	mergeDevice(devices, Device{UUID: "FF98F2F7AABBCCDDEEFF0011", Address: "192.0.2.10", Description: "http://192.0.2.10:59152/description.xml"})
	mergeDevice(devices, Device{UUID: "FF98F2F7AABBCCDDEEFF0011", Address: "192.0.2.10", Description: "http://192.0.2.10:49152/description.xml"})

	mustMatch(t, sorted(devices)[0].Description, "http://192.0.2.10:59152/description.xml")
}

// mergeDevice ignores a record with no UUID.
func TestMergeIgnoresAnEmptyIdentity(t *testing.T) {
	devices := map[string]Device{}
	mergeDevice(devices, Device{Name: "Amp", Address: "192.0.2.10"})
	mustMatch(t, devices, map[string]Device{})
}

// sorted orders devices by UUID, not by when they were found.
func TestSortedOrdersByUUID(t *testing.T) {
	got := sorted(map[string]Device{
		"BBB": {UUID: "BBB"},
		"AAA": {UUID: "AAA"},
		"CCC": {UUID: "CCC"},
	})
	mustMatch(t, []string{got[0].UUID, got[1].UUID, got[2].UUID}, []string{"AAA", "BBB", "CCC"})
}

// ssdpResponse is a renderer's response to the M-SEARCH.
var ssdpResponse = []byte("HTTP/1.1 200 OK\r\n" +
	"LOCATION: http://192.0.2.10:49152/device.xml\r\n" +
	"USN: uuid:ff98f2f7-aabb-ccdd-eeff-0011ff98f2f7::urn:schemas-upnp-org:device:MediaRenderer:1\r\n\r\n")

// searchSSDP sends the M-SEARCH to the SSDP group and reads a renderer's
// response.
func TestSearchSSDPReadsAResponder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		usePackets(t, func(conn *packetConn, _ []byte, to net.Addr) {
			if to.String() == ssdpGroup.String() {
				conn.answer(ssdpResponse)
			}
		})

		devices := map[string]Device{}
		searchSSDP(context.Background(), devices)
		got := sorted(devices)
		mustMatch(t, len(got), 1)
		mustMatch(t, got[0].UUID, "FF98F2F7AABBCCDDEEFF0011")
		mustMatch(t, got[0].Address, "192.0.2.10")
	})
}

// A renderer can wait up to the MX of the M-SEARCH before it responds,
// so the search keeps reading that long. The responder waits almost the
// whole MX from the request.
func TestSearchSSDPWaitsTheWholeMX(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		usePackets(t, func(conn *packetConn, packet []byte, _ net.Addr) {
			mx, _ := strconv.Atoi(httpHeaders(packet)["mx"])
			time.Sleep(time.Duration(mx)*time.Second - 100*time.Millisecond)
			conn.answer(ssdpResponse)
		})

		devices := map[string]Device{}
		searchSSDP(context.Background(), devices)

		mustMatch(t, len(devices), 1)
	})
}

// browseMDNS sends its query to the mDNS group and reads a responder's
// answer.
func TestBrowseMDNSReadsAResponder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		packet := mdnsDevicePacket(t, true)
		usePackets(t, func(conn *packetConn, _ []byte, to net.Addr) {
			if to.String() == mdnsGroup.String() {
				conn.answer(packet)
			}
		})

		devices := map[string]Device{}
		browseMDNS(context.Background(), devices)
		got := sorted(devices)
		mustMatch(t, len(got), 1)
		mustMatch(t, got[0].UUID, "FF98F2F7AABBCCDDEEFF0011")
		mustMatch(t, got[0].Name, "WiiM Amp")
		mustMatch(t, got[0].Address, "192.0.2.10")

		// The query is a PTR question for the LinkPlay service with the
		// unicast-response bit set.
		var message dnsmessage.Message
		if err := message.Unpack(mdnsQuery()); err != nil {
			t.Fatalf("unpacking the mDNS query: %v", err)
		}
		mustMatch(t, len(message.Questions), 1)
		mustMatch(t, message.Questions[0].Name.String(), linkplayService)
		mustMatch(t, message.Questions[0].Type, dnsmessage.TypePTR)
		mustMatch(t, message.Questions[0].Class, dnsmessage.Class(0x8001))
	})
}

// Discover returns an empty list when no device responds.
func TestDiscoverReturnsEmptyWhenNothingAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		usePackets(t, func(*packetConn, []byte, net.Addr) {})

		got := Discover(context.Background(), 3*time.Second)
		mustMatch(t, got, []Device{})
	})
}
