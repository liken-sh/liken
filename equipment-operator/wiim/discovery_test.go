// The discovery parsers and the merge, plus the SSDP and mDNS send and
// read paths against responders in memory. Multicast needs a real LAN
// and a real device, so each search sends from a packetConn, and a
// responder answers it with canned packets on the fake clock of a
// synctest bubble.

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

// packetConn is the socket a search opens, in memory. Each packet the
// search sends goes to respond, on a goroutine of its own, so a
// responder can wait before it answers, the way a renderer waits up to
// the MX. A read waits for an answer, the read deadline, or the close.
type packetConn struct {
	respond func(conn *packetConn, packet []byte, to net.Addr)
	inbox   chan []byte
	closed  chan struct{}
	once    sync.Once

	mutex    sync.Mutex
	deadline time.Time
}

// usePackets answers every socket a search opens with respond until the
// test ends.
func usePackets(t *testing.T, respond func(conn *packetConn, packet []byte, to net.Addr)) {
	t.Helper()
	restore := listenPacket
	listenPacket = func(string, string) (net.PacketConn, error) {
		return &packetConn{respond: respond, inbox: make(chan []byte, 16), closed: make(chan struct{})}, nil
	}
	t.Cleanup(func() { listenPacket = restore })
}

// answer queues one packet for the search to read. A socket the search
// has closed drops it.
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

// mdnsDevicePacket builds one mDNS response for an instance named
// "WiiM Amp". includeA decides whether the A record joins the PTR, SRV,
// and TXT records, so a test can split the address into its own packet.
func mdnsDevicePacket(t *testing.T, includeA bool) []byte {
	t.Helper()
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true})
	if err := builder.StartAnswers(); err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	if err := builder.SRVResource(
		header("WiiM Amp._linkplay._tcp.local."),
		dnsmessage.SRVResource{Port: 49152, Target: dnsmessage.MustNewName("WiiMAmp.local.")},
	); err != nil {
		t.Fatal(err)
	}
	if err := builder.TXTResource(
		header("WiiM Amp._linkplay._tcp.local."),
		dnsmessage.TXTResource{TXT: []string{"uuid=ff98f2f7-aabb-ccdd-eeff-0011ff98f2f7"}},
	); err != nil {
		t.Fatal(err)
	}
	if includeA {
		if err := builder.AResource(
			header("WiiMAmp.local."),
			dnsmessage.AResource{A: [4]byte{192, 0, 2, 10}},
		); err != nil {
			t.Fatal(err)
		}
	}
	packet, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return packet
}

// mdnsARecordPacket is a second packet that carries only the A record
// for the same host, the shape a responder uses when it splits the
// answer across several packets.
func mdnsARecordPacket(t *testing.T) []byte {
	t.Helper()
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true})
	if err := builder.StartAnswers(); err != nil {
		t.Fatal(err)
	}
	if err := builder.AResource(
		dnsmessage.ResourceHeader{
			Name:  dnsmessage.MustNewName("WiiMAmp.local."),
			Class: dnsmessage.ClassINET,
			TTL:   120,
		},
		dnsmessage.AResource{A: [4]byte{192, 0, 2, 10}},
	); err != nil {
		t.Fatal(err)
	}
	packet, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return packet
}

// The mDNS parser folds a packet with the PTR, SRV, TXT, and A records
// into one device.
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

// The mDNS parser assembles an instance whose records arrive in two
// packets: the identity and the name first, the address later.
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

// A packet that is not a DNS message folds nothing and cannot panic.
func TestParseMDNSIgnoresGarbage(t *testing.T) {
	instances := map[string]*mdnsInstance{}
	parseMDNS([]byte("not a dns packet"), instances)
	mustMatch(t, assembleMDNS(instances), []Device{})
}

// The SSDP parser reads the identity and the address from a response
// whose header casing and LOCATION path vary. The name is not in the
// response, so it stays empty.
func TestParseSSDPReadsEitherHeaderCasing(t *testing.T) {
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

// A response without an identity or without an address is not a device.
func TestParseSSDPRejectsWithoutIdentityOrAddress(t *testing.T) {
	if _, ok := parseSSDP([]byte("HTTP/1.1 200 OK\r\nLOCATION: http://192.0.2.10/x\r\n\r\n")); ok {
		t.Fatal("a response without a USN parsed")
	}
	if _, ok := parseSSDP([]byte("HTTP/1.1 200 OK\r\nUSN: uuid:FF98F2F7AABBCCDDEEFF0011\r\n\r\n")); ok {
		t.Fatal("a response without a LOCATION parsed")
	}
}

// An SSDP MediaRenderer that is not a LinkPlay device answers the same
// search and must not be taken for a WiiM.
func TestParseSSDPRejectsANonLinkPlayRenderer(t *testing.T) {
	response := []byte("HTTP/1.1 200 OK\r\n" +
		"LOCATION: http://192.0.2.20:60006/desc.xml\r\n" +
		"USN: uuid:508945DC-67F5-1B40-0080-000678F69EB2::urn:schemas-upnp-org:device:MediaRenderer:1\r\n\r\n")
	if _, ok := parseSSDP(response); ok {
		t.Fatal("a non-LinkPlay renderer parsed as a WiiM")
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

// The merge fills a missing name or address from whichever source
// carries it, and keeps one device per identity.
func TestMergeFillsMissingFields(t *testing.T) {
	devices := map[string]Device{}
	// SSDP finds the identity and the address, but no name.
	mergeDevice(devices, Device{UUID: "FF98F2F7AABBCCDDEEFF0011", Address: "192.0.2.11"})
	// mDNS finds the same identity with a name.
	mergeDevice(devices, Device{UUID: "FF98F2F7AABBCCDDEEFF0011", Name: "Amp", Address: "192.0.2.10"})

	got := sorted(devices)
	mustMatch(t, len(got), 1)
	mustMatch(t, got[0].Name, "Amp")
	mustMatch(t, got[0].Address, "192.0.2.11")
}

// The merge keeps the description URL the first source found, and
// fills it from the other source when the first had none.
func TestMergeFillsTheDescription(t *testing.T) {
	devices := map[string]Device{}
	mergeDevice(devices, Device{UUID: "FF98F2F7AABBCCDDEEFF0011", Address: "192.0.2.10"})
	mergeDevice(devices, Device{UUID: "FF98F2F7AABBCCDDEEFF0011", Address: "192.0.2.10", Description: "http://192.0.2.10:59152/description.xml"})
	mergeDevice(devices, Device{UUID: "FF98F2F7AABBCCDDEEFF0011", Address: "192.0.2.10", Description: "http://192.0.2.10:49152/description.xml"})

	mustMatch(t, sorted(devices)[0].Description, "http://192.0.2.10:59152/description.xml")
}

// A record with no identity never enters the set.
func TestMergeIgnoresAnEmptyIdentity(t *testing.T) {
	devices := map[string]Device{}
	mergeDevice(devices, Device{Name: "Amp", Address: "192.0.2.10"})
	mustMatch(t, devices, map[string]Device{})
}

// The sort orders devices by identity, not by arrival.
func TestSortedOrdersByUUID(t *testing.T) {
	got := sorted(map[string]Device{
		"BBB": {UUID: "BBB"},
		"AAA": {UUID: "AAA"},
		"CCC": {UUID: "CCC"},
	})
	mustMatch(t, []string{got[0].UUID, got[1].UUID, got[2].UUID}, []string{"AAA", "BBB", "CCC"})
}

// ssdpResponse is a renderer's answer to the M-SEARCH.
var ssdpResponse = []byte("HTTP/1.1 200 OK\r\n" +
	"LOCATION: http://192.0.2.10:49152/device.xml\r\n" +
	"USN: uuid:ff98f2f7-aabb-ccdd-eeff-0011ff98f2f7::urn:schemas-upnp-org:device:MediaRenderer:1\r\n\r\n")

// The SSDP search sends its M-SEARCH to the SSDP group, and reads the
// answer of a renderer.
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

// A renderer may wait any time up to the MX the M-SEARCH states before
// it answers, so the read stays open that long. The responder waits
// almost the whole MX it reads from the request.
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

// The mDNS browse sends its query to the mDNS group, reads the answer
// of a responder, and the query it sends is a valid DNS message.
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

		// The query the browse sends is a PTR question for the LinkPlay
		// service with the unicast-response bit set.
		var message dnsmessage.Message
		if err := message.Unpack(mdnsQuery()); err != nil {
			t.Fatal(err)
		}
		mustMatch(t, len(message.Questions), 1)
		mustMatch(t, message.Questions[0].Name.String(), linkplayService)
		mustMatch(t, message.Questions[0].Type, dnsmessage.TypePTR)
		mustMatch(t, message.Questions[0].Class, dnsmessage.Class(0x8001))
	})
}

// Discover returns nothing when nothing answers. Both paths search
// until the window closes, and return an empty list.
func TestDiscoverReturnsEmptyWhenNothingAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		usePackets(t, func(*packetConn, []byte, net.Addr) {})

		got := Discover(context.Background(), 3*time.Second)
		mustMatch(t, got, []Device{})
	})
}
